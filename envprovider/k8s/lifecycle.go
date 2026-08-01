package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	ultra "github.com/aleksclark/ultralogical"
)

// handleVersion is the persisted handle schema version.
const handleVersion = 1

func encodeHandle(d handleData) (ultra.ProviderHandle, error) {
	body, err := json.Marshal(d)
	if err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("k8s: encode handle: %w", err)
	}
	return ultra.ProviderHandle{Version: handleVersion, Data: body}, nil
}

func decodeHandle(h ultra.ProviderHandle) (handleData, error) {
	var d handleData
	if len(h.Data) == 0 {
		return d, errors.New("k8s: empty provider handle")
	}
	if err := json.Unmarshal(h.Data, &d); err != nil {
		return d, fmt.Errorf("k8s: decode handle: %w", err)
	}
	return d, nil
}

// Provision creates the environment's Secret, Pod, and Service. Creation is
// idempotent by name: a redelivered provisioning adopts what it already made
// rather than failing or duplicating.
func (p *Provider) Provision(ctx context.Context, envID ultra.EnvID, spec ultra.EnvSpec, token string) (ultra.ProviderHandle, error) {
	namespace := p.Namespace()
	if err := p.ensureNamespace(ctx, namespace); err != nil {
		return ultra.ProviderHandle{}, err
	}
	// The quota is enforced before anything is created: a rejected provision
	// must leave no partial resources behind.
	if err := p.enforceQuota(ctx, namespace, envID); err != nil {
		return ultra.ProviderHandle{}, err
	}
	name := objectName(envID)
	if err := p.applySecret(ctx, namespace, name, envID, token); err != nil {
		return ultra.ProviderHandle{}, err
	}
	if err := p.applyPod(ctx, namespace, name, envID, spec); err != nil {
		return ultra.ProviderHandle{}, err
	}
	service, err := p.applyService(ctx, namespace, name, envID)
	if err != nil {
		return ultra.ProviderHandle{}, err
	}
	data := handleData{Namespace: namespace, Name: name, EnvID: string(envID)}
	if p.cfg.EndpointMode == EndpointModeNodePort && len(service.Spec.Ports) > 0 {
		data.NodePort = service.Spec.Ports[0].NodePort
	}
	return encodeHandle(data)
}

// ensureNamespace creates the environment namespace, and in hosted mode the
// isolation policy that goes with it. A hosted org's boundary is created with
// its namespace so no environment can exist outside one.
func (p *Provider) ensureNamespace(ctx context.Context, namespace string) error {
	desired := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   namespace,
		Labels: map[string]string{LabelManagedBy: ManagedByValue, "ultralogical.dev/org-id": sanitize(p.cfg.OrgID)},
	}}
	_, err := p.client.CoreV1().Namespaces().Create(ctx, desired, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("k8s: create namespace %s: %w", namespace, err)
	}
	if !p.cfg.Hosted {
		return nil
	}
	return p.applyHostedPolicy(ctx, namespace)
}

// enforceQuota rejects a provision that would exceed the hosted ceiling. The
// check counts live pods rather than trusting a cached number, because the
// ceiling is about what is actually running.
func (p *Provider) enforceQuota(ctx context.Context, namespace string, envID ultra.EnvID) error {
	if !p.cfg.Hosted {
		return nil
	}
	pods, err := p.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelManagedBy + "=" + ManagedByValue,
	})
	if err != nil {
		return fmt.Errorf("k8s: count environments: %w", err)
	}
	live := 0
	for _, pod := range pods.Items {
		if pod.Labels[LabelEnvID] == sanitize(string(envID)) {
			// Re-provisioning an environment that already exists is adoption,
			// not a new allocation against the ceiling.
			return nil
		}
		if pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
			live++
		}
	}
	if live >= p.cfg.MaxEnvironments {
		return &QuotaError{Namespace: namespace, Limit: p.cfg.MaxEnvironments, Running: live}
	}
	return nil
}

// QuotaError is the typed refusal a caller receives when an org is at its
// concurrent-environment ceiling. It is deliberately not a generic error: the
// caller has to be able to tell "you are at your limit" from "the cluster
// broke".
type QuotaError struct {
	Namespace string
	Limit     int
	Running   int
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("environment quota reached: %d of %d in use", e.Running, e.Limit)
}

func (p *Provider) applySecret(ctx context.Context, namespace, name string, envID ultra.EnvID, token string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: p.labels(envID)},
		StringData: map[string]string{"token": token},
	}
	_, err := p.client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// The token may have rotated, so the existing secret is updated rather
		// than left holding a credential nothing accepts.
		_, err = p.client.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("k8s: apply token secret: %w", err)
	}
	return nil
}

func (p *Provider) quantity(value, fallback string) resource.Quantity {
	if value == "" {
		value = fallback
	}
	parsed, err := resource.ParseQuantity(value)
	if err != nil {
		return resource.MustParse(fallback)
	}
	return parsed
}

func (p *Provider) applyPod(ctx context.Context, namespace, name string, envID ultra.EnvID, spec ultra.EnvSpec) error {
	workdir := spec.Workdir
	if workdir == "" {
		workdir = "/work"
	}
	image := p.cfg.Image
	if spec.Image != "" {
		image = spec.Image
	}
	env := []corev1.EnvVar{{
		Name: "BEZALEL_AUTH_TOKEN",
		ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: "token",
		}},
	}}
	for key, value := range spec.Env {
		env = append(env, corev1.EnvVar{Name: key, Value: value})
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: p.labels(envID)},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:  "bezalel",
				Image: image,
				Args:  []string{"--workdir", workdir},
				Env:   env,
				Ports: []corev1.ContainerPort{{Name: "tools", ContainerPort: toolPort}},
				// Readiness is the environment agent's own health endpoint, so
				// "ready" means the tool surface answers rather than that a
				// container started.
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
						Path: "/health", Port: intstr.FromInt32(toolPort),
					}},
					InitialDelaySeconds: 1,
					PeriodSeconds:       2,
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    p.quantity(p.cfg.CPURequest, "100m"),
						corev1.ResourceMemory: p.quantity(p.cfg.MemoryRequest, "256Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    p.quantity(p.cfg.CPULimit, "1"),
						corev1.ResourceMemory: p.quantity(p.cfg.MemoryLimit, "1Gi"),
					},
				},
				VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: workdir}},
			}},
			Volumes: []corev1.Volume{{
				Name:         "workspace",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			}},
		},
	}
	_, err := p.client.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// Adoption: the pod this environment owns already exists, which is
		// what a redelivered provisioning should find.
		return nil
	}
	if err != nil {
		return fmt.Errorf("k8s: create pod: %w", err)
	}
	return nil
}

func (p *Provider) applyService(ctx context.Context, namespace, name string, envID ultra.EnvID) (*corev1.Service, error) {
	serviceType := corev1.ServiceTypeClusterIP
	if p.cfg.EndpointMode == EndpointModeNodePort {
		serviceType = corev1.ServiceTypeNodePort
	}
	port := corev1.ServicePort{Name: "tools", Port: toolPort, TargetPort: intstr.FromInt32(toolPort)}
	// Existing services are reused before a port is chosen, so adoption never
	// consumes a second port from a bounded range.
	if existing, err := p.client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{}); err == nil {
		return existing, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("k8s: read service: %w", err)
	}
	if serviceType == corev1.ServiceTypeNodePort && p.cfg.NodePortRange[1] > 0 {
		assigned, err := p.freeNodePort(ctx)
		if err != nil {
			return nil, err
		}
		port.NodePort = assigned
	}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: p.labels(envID)},
		Spec: corev1.ServiceSpec{
			Type:     serviceType,
			Selector: p.labels(envID),
			Ports:    []corev1.ServicePort{port},
		},
	}
	created, err := p.client.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		created, err = p.client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("k8s: apply service: %w", err)
	}
	return created, nil
}

// freeNodePort picks a port from the configured range that no service is
// already using. A deployment that forwards a fixed range cannot accept a
// random assignment, and silently taking one would produce an endpoint no
// worker can reach.
func (p *Provider) freeNodePort(ctx context.Context) (int32, error) {
	services, err := p.client.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("k8s: list services for port assignment: %w", err)
	}
	taken := map[int32]bool{}
	for _, service := range services.Items {
		for _, port := range service.Spec.Ports {
			if port.NodePort != 0 {
				taken[port.NodePort] = true
			}
		}
	}
	for candidate := p.cfg.NodePortRange[0]; candidate <= p.cfg.NodePortRange[1]; candidate++ {
		if !taken[candidate] {
			return candidate, nil
		}
	}
	return 0, fmt.Errorf("k8s: no free node port in range %d-%d", p.cfg.NodePortRange[0], p.cfg.NodePortRange[1])
}

// Status maps the pod's real condition onto the environment state machine. A
// pod that vanished is failed, not merely unready: something outside the
// platform removed it, and reconciliation has to see that.
func (p *Provider) Status(ctx context.Context, handle ultra.ProviderHandle) (ultra.ProviderStatus, error) {
	d, err := decodeHandle(handle)
	if err != nil {
		return ultra.ProviderStatus{}, err
	}
	pod, err := p.client.CoreV1().Pods(d.Namespace).Get(ctx, d.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return ultra.ProviderStatus{State: ultra.EnvFailed, Message: "pod not found"}, nil
	}
	if err != nil {
		return ultra.ProviderStatus{}, fmt.Errorf("k8s: get pod: %w", err)
	}
	switch pod.Status.Phase {
	case corev1.PodRunning:
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return ultra.ProviderStatus{State: ultra.EnvReady}, nil
			}
		}
		return ultra.ProviderStatus{State: ultra.EnvProvisioning, Message: "pod is not ready"}, nil
	case corev1.PodPending:
		return ultra.ProviderStatus{State: ultra.EnvProvisioning, Message: podPendingReason(pod)}, nil
	case corev1.PodSucceeded, corev1.PodFailed:
		return ultra.ProviderStatus{State: ultra.EnvFailed, Message: string(pod.Status.Phase)}, nil
	default:
		return ultra.ProviderStatus{State: ultra.EnvProvisioning}, nil
	}
}

// podPendingReason surfaces why a pod has not started, so an operator sees
// "image cannot be pulled" rather than an opaque wait.
func podPendingReason(pod *corev1.Pod) string {
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting != nil && status.State.Waiting.Reason != "" {
			return status.State.Waiting.Reason
		}
	}
	return "pending"
}

// Endpoint publishes the environment's tool endpoint from the Service, which
// is discovery rather than a guess: a Service that was never created, or whose
// node port was never assigned, yields an error instead of a plausible URL.
func (p *Provider) Endpoint(ctx context.Context, handle ultra.ProviderHandle) (string, error) {
	d, err := decodeHandle(handle)
	if err != nil {
		return "", err
	}
	service, err := p.client.CoreV1().Services(d.Namespace).Get(ctx, d.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("k8s: get service: %w", err)
	}
	if len(service.Spec.Ports) == 0 {
		return "", errors.New("k8s: service publishes no port")
	}
	// The endpoint contract is the tool path itself: callers pass it straight
	// to an MCP client, and health checks derive /health from it.
	if p.cfg.EndpointMode == EndpointModeNodePort {
		port := service.Spec.Ports[0].NodePort
		if port == 0 {
			return "", errors.New("k8s: service has no assigned node port yet")
		}
		host := p.cfg.EndpointHost
		if host == "" {
			host = "127.0.0.1"
		}
		return fmt.Sprintf("http://%s:%d/mcp", host, port), nil
	}
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/mcp", service.Name, service.Namespace, toolPort), nil
}

// Restart replaces the pod with a new one carrying the rotated token. The
// workspace is an emptyDir, so it does not survive; the provider does not
// claim CapabilityRestartPreservesWorkspace and the conformance suite asserts
// accordingly rather than being told to skip the step.
func (p *Provider) Restart(ctx context.Context, envID ultra.EnvID, handle ultra.ProviderHandle, spec ultra.EnvSpec, token string) (ultra.ProviderHandle, error) {
	d, err := decodeHandle(handle)
	if err != nil {
		return ultra.ProviderHandle{}, err
	}
	policy := metav1.DeletePropagationForeground
	grace := int64(0)
	err = p.client.CoreV1().Pods(d.Namespace).Delete(ctx, d.Name, metav1.DeleteOptions{
		PropagationPolicy: &policy, GracePeriodSeconds: &grace,
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return ultra.ProviderHandle{}, fmt.Errorf("k8s: delete pod for restart: %w", err)
	}
	// The replacement cannot be created until the old object is gone, or the
	// API server rejects it as already existing.
	if err := p.awaitPodAbsent(ctx, d.Namespace, d.Name); err != nil {
		return ultra.ProviderHandle{}, err
	}
	return p.Provision(ctx, envID, spec, token)
}

func (p *Provider) awaitPodAbsent(ctx context.Context, namespace, name string) error {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		_, err := p.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("k8s: await pod deletion: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("k8s: pod was not deleted within the deadline")
}

// Terminate removes every object this provider created for the environment.
// It is idempotent: deleting what is already gone is success, because a retry
// must not turn a completed cleanup into a failure.
func (p *Provider) Terminate(ctx context.Context, handle ultra.ProviderHandle) error {
	d, err := decodeHandle(handle)
	if err != nil {
		// A handle that was never written means nothing was created.
		return nil
	}
	policy := metav1.DeletePropagationForeground
	grace := int64(0)
	options := metav1.DeleteOptions{PropagationPolicy: &policy, GracePeriodSeconds: &grace}
	if err := p.client.CoreV1().Pods(d.Namespace).Delete(ctx, d.Name, options); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("k8s: delete pod: %w", err)
	}
	if err := p.client.CoreV1().Services(d.Namespace).Delete(ctx, d.Name, options); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("k8s: delete service: %w", err)
	}
	if err := p.client.CoreV1().Secrets(d.Namespace).Delete(ctx, d.Name, options); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("k8s: delete secret: %w", err)
	}
	return nil
}

// Adopt implements ultra.EnvAdopter: it finds resources this provider already
// created for an environment, so provisioning interrupted between creation and
// handle persistence resumes instead of creating a second pod.
func (p *Provider) Adopt(ctx context.Context, envID ultra.EnvID) (ultra.ProviderHandle, bool, error) {
	namespace := p.Namespace()
	pods, err := p.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: p.selector(envID)})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ultra.ProviderHandle{}, false, nil
		}
		return ultra.ProviderHandle{}, false, fmt.Errorf("k8s: adopt lookup: %w", err)
	}
	if len(pods.Items) == 0 {
		return ultra.ProviderHandle{}, false, nil
	}
	name := objectName(envID)
	data := handleData{Namespace: namespace, Name: name, EnvID: string(envID)}
	if service, err := p.client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{}); err == nil {
		if len(service.Spec.Ports) > 0 {
			data.NodePort = service.Spec.Ports[0].NodePort
		}
	}
	handle, err := encodeHandle(data)
	return handle, err == nil, err
}

// Resources implements ultra.EnvResourceLister by enumerating the live
// Kubernetes objects this provider owns for an environment. It is what turns
// "terminated" into a checkable claim.
func (p *Provider) Resources(ctx context.Context, envID ultra.EnvID) ([]string, error) {
	namespace := p.Namespace()
	selector := metav1.ListOptions{LabelSelector: p.selector(envID)}
	var out []string
	pods, err := p.client.CoreV1().Pods(namespace).List(ctx, selector)
	if err != nil {
		return nil, fmt.Errorf("k8s: list pods: %w", err)
	}
	for _, pod := range pods.Items {
		out = append(out, "pod/"+pod.Namespace+"/"+pod.Name)
	}
	services, err := p.client.CoreV1().Services(namespace).List(ctx, selector)
	if err != nil {
		return nil, fmt.Errorf("k8s: list services: %w", err)
	}
	for _, service := range services.Items {
		out = append(out, "service/"+service.Namespace+"/"+service.Name)
	}
	secrets, err := p.client.CoreV1().Secrets(namespace).List(ctx, selector)
	if err != nil {
		return nil, fmt.Errorf("k8s: list secrets: %w", err)
	}
	for _, secret := range secrets.Items {
		out = append(out, "secret/"+secret.Namespace+"/"+secret.Name)
	}
	return out, nil
}
