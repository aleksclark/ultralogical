package k8s

import (
	"context"
	"fmt"
	"net"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ultra "github.com/aleksclark/ultralogical"
)

// applyHostedPolicy creates the hard boundary a hosted org's namespace must
// have before it can hold an environment: a service account with no cluster
// authority, a NetworkPolicy that denies traffic from other namespaces, and a
// ResourceQuota. These are created with the namespace rather than after it, so
// an environment can never exist inside an unbounded namespace.
func (p *Provider) applyHostedPolicy(ctx context.Context, namespace string) error {
	if err := p.applyServiceAccountAndRole(ctx, namespace); err != nil {
		return err
	}
	if err := p.applyNetworkPolicy(ctx, namespace); err != nil {
		return err
	}
	return p.applyResourceQuota(ctx, namespace)
}

func (p *Provider) applyServiceAccountAndRole(ctx context.Context, namespace string) error {
	labels := map[string]string{LabelManagedBy: ManagedByValue}
	account := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "ultra-env", Namespace: namespace, Labels: labels},
	}
	if _, err := p.client.CoreV1().ServiceAccounts(namespace).Create(ctx, account, metav1.CreateOptions{}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("k8s: create service account: %w", err)
	}
	// The role is deliberately empty: an environment needs no Kubernetes API
	// access at all, and binding a named role with no rules makes that an
	// explicit, inspectable decision rather than an omission.
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "ultra-env", Namespace: namespace, Labels: labels},
		Rules:      []rbacv1.PolicyRule{},
	}
	if _, err := p.client.RbacV1().Roles(namespace).Create(ctx, role, metav1.CreateOptions{}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("k8s: create role: %w", err)
	}
	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "ultra-env", Namespace: namespace, Labels: labels},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: "ultra-env", Namespace: namespace}},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "ultra-env"},
	}
	if _, err := p.client.RbacV1().RoleBindings(namespace).Create(ctx, binding, metav1.CreateOptions{}); err != nil &&
		!apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("k8s: create role binding: %w", err)
	}
	return nil
}

// applyNetworkPolicy denies ingress from every namespace but this one. Two
// hosted orgs must not be able to reach each other's environments even though
// they share a cluster.
func (p *Provider) applyNetworkPolicy(ctx context.Context, namespace string) error {
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ultra-env-isolation", Namespace: namespace,
			Labels: map[string]string{LabelManagedBy: ManagedByValue},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{LabelManagedBy: ManagedByValue}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     []networkingv1.NetworkPolicyIngressRule{{From: p.allowedIngress(namespace)}},
		},
	}
	_, err := p.client.NetworkingV1().NetworkPolicies(namespace).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("k8s: create network policy: %w", err)
	}
	return nil
}

// allowedIngress names exactly who may reach an org's environments: the
// org's own namespace, and the platform ranges that drive them. Every other
// namespace in the cluster is excluded, which is the isolation guarantee;
// omitting the platform ranges would instead produce an environment nothing
// can use.
func (p *Provider) allowedIngress(namespace string) []networkingv1.NetworkPolicyPeer {
	peers := []networkingv1.NetworkPolicyPeer{{
		NamespaceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"kubernetes.io/metadata.name": namespace},
		},
	}}
	for _, cidr := range p.cfg.PlatformIngressCIDRs {
		peers = append(peers, networkingv1.NetworkPolicyPeer{
			IPBlock: &networkingv1.IPBlock{
				CIDR: cidr,
				// Pod traffic is excluded from every platform range. A range
				// broad enough to include the cluster's own pod network would
				// otherwise re-admit the neighbouring orgs the policy exists
				// to exclude.
				Except: p.podCIDRs,
			},
		})
	}
	return peers
}

// validatePlatformIngress refuses a range that would defeat isolation. An
// operator who allows the whole internet has not configured a boundary, and
// discovering that from a passing test later is worse than failing now.
func validatePlatformIngress(cidrs []string, podCIDRs []string) error {
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("k8s: platform ingress %q is not a CIDR: %w", cidr, err)
		}
		ones, bits := network.Mask.Size()
		if ones == 0 && bits > 0 && len(podCIDRs) == 0 {
			return fmt.Errorf("k8s: platform ingress %q admits every address and no pod range is excluded", cidr)
		}
	}
	return nil
}

func (p *Provider) applyResourceQuota(ctx context.Context, namespace string) error {
	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ultra-env-quota", Namespace: namespace,
			Labels: map[string]string{LabelManagedBy: ManagedByValue},
		},
		Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{
			corev1.ResourcePods: *resource.NewQuantity(int64(p.cfg.MaxEnvironments), resource.DecimalSI),
		}},
	}
	_, err := p.client.CoreV1().ResourceQuotas(namespace).Create(ctx, quota, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("k8s: create resource quota: %w", err)
	}
	return nil
}

// Probe implements ultra.CapabilityProber. It asks the cluster what it can
// actually do rather than inferring capability from the provider kind: a
// cluster whose API server does not serve NetworkPolicy reports the absence,
// so a flow depending on isolation is refused instead of silently unprotected.
func (p *Provider) Probe(ctx context.Context) (ultra.ProviderCapabilities, error) {
	capabilities := ultra.ProviderCapabilities{
		Kind:  p.kind(),
		Notes: map[ultra.ProviderCapability]string{},
	}
	// Reachability first: an unreachable control plane is an error, not a
	// provider with no capabilities.
	if _, err := p.client.Discovery().ServerVersion(); err != nil {
		return capabilities, fmt.Errorf("k8s: control plane unreachable: %w", err)
	}
	// Creating pods is the minimum. Without it there is no provider.
	if err := p.canCreate(ctx, "pods"); err != nil {
		return capabilities, fmt.Errorf("k8s: cannot create pods: %w", err)
	}
	capabilities.Supported = append(capabilities.Supported,
		ultra.CapabilityAdoptsOrphans,
		ultra.CapabilityEnumeratesResources,
	)
	// Serving the tool endpoint requires Services.
	if err := p.canCreate(ctx, "services"); err == nil {
		capabilities.Supported = append(capabilities.Supported, ultra.CapabilityServesToolEndpoint)
	} else {
		capabilities.Notes[ultra.CapabilityServesToolEndpoint] =
			"the cluster does not allow creating Services, so no tool endpoint can be published"
	}
	if err := p.canCreate(ctx, "resourcequotas"); err == nil {
		capabilities.Supported = append(capabilities.Supported, ultra.CapabilityResourceQuota)
	} else {
		capabilities.Notes[ultra.CapabilityResourceQuota] =
			"the cluster does not allow creating ResourceQuotas"
	}
	if p.servesNetworkPolicy() {
		capabilities.Supported = append(capabilities.Supported, ultra.CapabilityNamespaceIsolation)
	} else {
		capabilities.Notes[ultra.CapabilityNamespaceIsolation] =
			"the cluster does not serve the NetworkPolicy API, so namespaces cannot be isolated"
	}
	return capabilities, nil
}

func (p *Provider) kind() string {
	if p.cfg.Hosted {
		return ultra.ProviderKindHostedEKS
	}
	return ultra.ProviderKindBYOKubernetes
}

// canCreate performs a read-only permission check for one resource. It is a
// dry run: nothing is created, so a failed registration leaves no trace in the
// operator's cluster.
func (p *Provider) canCreate(ctx context.Context, resourceName string) error {
	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: p.Namespace(), Verb: "create", Resource: resourceName,
			},
		},
	}
	result, err := p.client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("permission check for %s failed: %w", resourceName, err)
	}
	if !result.Status.Allowed {
		return fmt.Errorf("not permitted to create %s: %s", resourceName, result.Status.Reason)
	}
	return nil
}

// servesNetworkPolicy reports whether the cluster exposes the NetworkPolicy
// API at all. A cluster without it cannot isolate namespaces regardless of
// permissions.
func (p *Provider) servesNetworkPolicy() bool {
	groups, err := p.client.Discovery().ServerGroups()
	if err != nil {
		return false
	}
	for _, group := range groups.Groups {
		if group.Name == networkingv1.GroupName {
			return true
		}
	}
	return false
}
