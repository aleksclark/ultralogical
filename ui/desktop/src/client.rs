//! The desktop's connection to ultrad. Every action the window can perform is
//! a method here, and the native entrypoint and the UI tests both drive the
//! window through the same methods — there is no test-only API path.
//!
//! GPUI owns the UI executor and tonic requires a Tokio reactor, so the client
//! carries a Tokio runtime handle and dispatches every request onto it. That
//! keeps network work off the render thread and lets the same client run under
//! the native application and under GPUI's test executor unchanged.

use std::collections::HashMap;
use std::future::Future;
use std::sync::mpsc::{Receiver, Sender, channel};
use tokio::runtime::Handle;
use tonic::transport::Channel;
use ultralogical_client::{Auth, ultra::v1};

use crate::state::{
    CredentialView, EnvHostView, FlowFieldErrorView, FlowInvocationView, FlowSummary, ProviderView,
    SessionSummary, UsageView,
};

pub type BoxError = Box<dyn std::error::Error + Send + Sync>;

/// DesktopClient owns the generated tonic clients for one authenticated user.
#[derive(Clone)]
pub struct DesktopClient {
    auth: Auth,
    runtime: Handle,
    pub orgs: v1::org_service_client::OrgServiceClient<Channel>,
    pub sessions: v1::session_service_client::SessionServiceClient<Channel>,
    pub events: v1::event_service_client::EventServiceClient<Channel>,
    pub agents: v1::agent_service_client::AgentServiceClient<Channel>,
    pub envs: v1::env_service_client::EnvServiceClient<Channel>,
    pub billing: v1::billing_service_client::BillingServiceClient<Channel>,
    pub flows: v1::flow_service_client::FlowServiceClient<Channel>,
}

impl DesktopClient {
    /// connect dials ultrad on the given Tokio runtime.
    pub async fn connect(runtime: Handle, url: String, token: &str) -> Result<Self, BoxError> {
        let dial = runtime.clone();
        let channel = dial
            .spawn(async move { Channel::from_shared(url)?.connect().await.map_err(BoxError::from) })
            .await??;
        Ok(Self {
            auth: Auth::new(token)?,
            runtime,
            orgs: v1::org_service_client::OrgServiceClient::new(channel.clone()),
            sessions: v1::session_service_client::SessionServiceClient::new(channel.clone()),
            events: v1::event_service_client::EventServiceClient::new(channel.clone()),
            agents: v1::agent_service_client::AgentServiceClient::new(channel.clone()),
            envs: v1::env_service_client::EnvServiceClient::new(channel.clone()),
            billing: v1::billing_service_client::BillingServiceClient::new(channel.clone()),
            flows: v1::flow_service_client::FlowServiceClient::new(channel),
        })
    }

    pub fn auth<T>(&self, value: T) -> tonic::Request<T> {
        self.auth.request(value)
    }

    pub fn runtime(&self) -> Handle {
        self.runtime.clone()
    }

    /// dispatch runs one request on the Tokio runtime and awaits its result on
    /// the caller's executor.
    async fn dispatch<F, T>(&self, future: F) -> Result<T, BoxError>
    where
        F: Future<Output = Result<T, BoxError>> + Send + 'static,
        T: Send + 'static,
    {
        self.runtime.spawn(future).await?
    }

    pub async fn list_sessions(&mut self, org_id: &str) -> Result<Vec<SessionSummary>, BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        self.dispatch(async move {
            let resp = client
                .sessions
                .list_sessions(client.auth.request(v1::ListSessionsRequest { org_id }))
                .await?
                .into_inner();
            Ok(resp
                .sessions
                .into_iter()
                .map(|s| SessionSummary { id: s.id, title: s.title })
                .collect())
        })
        .await
    }

    pub async fn create_session(&mut self, org_id: &str, title: &str) -> Result<SessionSummary, BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        let title = title.to_string();
        self.dispatch(async move {
            let session = client
                .sessions
                .create_session(client.auth.request(v1::CreateSessionRequest { org_id, title }))
                .await?
                .into_inner()
                .session
                .ok_or_else(|| BoxError::from("create_session returned no session"))?;
            Ok(SessionSummary { id: session.id, title: session.title })
        })
        .await
    }

    pub async fn join(&mut self, session_id: &str, display: &str) -> Result<(), BoxError> {
        let mut client = self.clone();
        let session_id = session_id.to_string();
        let display = display.to_string();
        self.dispatch(async move {
            client
                .sessions
                .join(client.auth.request(v1::JoinRequest { session_id, display }))
                .await?;
            Ok(())
        })
        .await
    }

    pub async fn set_memory(&mut self, session_id: &str, key: &str, value_json: &str) -> Result<(), BoxError> {
        let mut client = self.clone();
        let session_id = session_id.to_string();
        let key = key.to_string();
        let value_json = value_json.to_string();
        self.dispatch(async move {
            client
                .sessions
                .set_memory(client.auth.request(v1::SetMemoryRequest { session_id, key, value_json }))
                .await?;
            Ok(())
        })
        .await
    }

    pub async fn list_memory(&mut self, session_id: &str) -> Result<Vec<String>, BoxError> {
        let mut client = self.clone();
        let session_id = session_id.to_string();
        self.dispatch(async move {
            let listed = client
                .sessions
                .list_memory(client.auth.request(v1::ListMemoryRequest { session_id }))
                .await?
                .into_inner();
            Ok(listed.entries.into_iter().map(|entry| entry.key).collect())
        })
        .await
    }

    pub async fn start_run(&mut self, session_id: &str, prompt: &str) -> Result<String, BoxError> {
        let mut client = self.clone();
        let session_id = session_id.to_string();
        let prompt = prompt.to_string();
        self.dispatch(async move {
            let run = client
                .agents
                .start_run(client.auth.request(v1::StartRunRequest {
                    session_id,
                    prompt,
                    model_config: None,
                    grants: None,
                }))
                .await?
                .into_inner()
                .run
                .ok_or_else(|| BoxError::from("start_run returned no run"))?;
            Ok(run.id)
        })
        .await
    }

    pub async fn append_user_message(&mut self, session_id: &str, text: &str) -> Result<(), BoxError> {
        let mut client = self.clone();
        let session_id = session_id.to_string();
        let text = text.to_string();
        self.dispatch(async move {
            client
                .events
                .append(client.auth.request(v1::AppendRequest {
                    session_id,
                    payload: Some(v1::EventPayload {
                        payload: Some(v1::event_payload::Payload::UserMessage(v1::UserMessage { text })),
                    }),
                }))
                .await?;
            Ok(())
        })
        .await
    }

    pub async fn answer(&mut self, run_id: &str, message: &str) -> Result<(), BoxError> {
        let mut client = self.clone();
        let run_id = run_id.to_string();
        let message = message.to_string();
        self.dispatch(async move {
            client
                .agents
                .prompt_run(client.auth.request(v1::PromptRunRequest { run_id, message }))
                .await?;
            Ok(())
        })
        .await
    }

    pub async fn provision_env(&mut self, session_id: &str, name: &str) -> Result<String, BoxError> {
        let mut client = self.clone();
        let session_id = session_id.to_string();
        let name = name.to_string();
        self.dispatch(async move {
            let env = client
                .envs
                .provision_env(client.auth.request(v1::ProvisionEnvRequest {
                    session_id,
                    spec: Some(v1::EnvSpec {
                        name,
                        image: String::new(),
                        workdir: "/work".into(),
                        env: HashMap::new(),
                        metadata: HashMap::new(),
                    }),
                    provider_instance: "default".into(),
                }))
                .await?
                .into_inner()
                .env
                .ok_or_else(|| BoxError::from("provision_env returned no env"))?;
            Ok(env.id)
        })
        .await
    }

    pub async fn restart_env(&mut self, env_id: &str) -> Result<i32, BoxError> {
        let mut client = self.clone();
        let env_id = env_id.to_string();
        self.dispatch(async move {
            let env = client
                .envs
                .restart_env(client.auth.request(v1::RestartEnvRequest { env_id }))
                .await?
                .into_inner()
                .env
                .ok_or_else(|| BoxError::from("restart_env returned no env"))?;
            Ok(env.epoch)
        })
        .await
    }

    pub async fn terminate_env(&mut self, env_id: &str) -> Result<(), BoxError> {
        let mut client = self.clone();
        let env_id = env_id.to_string();
        self.dispatch(async move {
            client
                .envs
                .terminate_env(client.auth.request(v1::TerminateEnvRequest { env_id }))
                .await?;
            Ok(())
        })
        .await
    }

    pub async fn exec_preview(&mut self, env_id: &str, command: &str) -> Result<String, BoxError> {
        let mut client = self.clone();
        let env_id = env_id.to_string();
        let command = command.to_string();
        self.dispatch(async move {
            let resp = client
                .envs
                .exec_preview(client.auth.request(v1::ExecPreviewRequest { env_id, command }))
                .await?
                .into_inner();
            Ok(resp.output)
        })
        .await
    }

    pub async fn usage(&mut self, org_id: &str) -> Result<(Vec<UsageView>, i64), BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        self.dispatch(async move {
            let resp = client
                .billing
                .get_usage(client.auth.request(v1::GetUsageRequest { org_id, from: None, to: None }))
                .await?
                .into_inner();
            let intervals = resp
                .intervals
                .into_iter()
                .map(|i| UsageView {
                    env_id: i.env_id,
                    seconds: i.seconds,
                    rate_class: i.rate_class,
                    open: i.open,
                })
                .collect();
            Ok((intervals, resp.total_seconds))
        })
        .await
    }

    /// run_tree fetches the session's spawn tree. The whole shape arrives at
    /// once because walking it request by request would race the live stream.
    pub async fn run_tree(&mut self, session_id: &str) -> Result<Vec<crate::state::RunNode>, BoxError> {
        let mut client = self.clone();
        let session_id = session_id.to_string();
        self.dispatch(async move {
            let resp = client
                .agents
                .get_run_tree(client.auth.request(v1::GetRunTreeRequest { session_id }))
                .await?
                .into_inner();
            Ok(crate::state::DesktopState::flatten_tree(&resp.roots))
        })
        .await
    }

    /// put_credential stores an inference credential. The gateway fields are
    /// part of the contract: an org routing inference through a gateway needs
    /// a base URL and extra headers, not just a key.
    pub async fn put_credential(
        &mut self,
        org_id: &str,
        name: &str,
        api_key: &str,
        base_url: &str,
        extra_headers_json: &str,
    ) -> Result<CredentialView, BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        let name = name.to_string();
        let api_key = api_key.to_string();
        let base_url = base_url.to_string();
        let extra_headers_json = extra_headers_json.to_string();
        self.dispatch(async move {
            let credential = client
                .orgs
                .put_credential(client.auth.request(v1::PutCredentialRequest {
                    org_id,
                    kind: "inference:openai".into(),
                    name,
                    api_key,
                    base_url,
                    extra_headers_json,
                }))
                .await?
                .into_inner()
                .credential
                .ok_or_else(|| BoxError::from("put_credential returned no credential"))?;
            Ok(CredentialView { kind: credential.kind, name: credential.name })
        })
        .await
    }

    /// list_credentials fetches the org's credentials by identity. The API
    /// never returns secret material, so neither can this.
    pub async fn list_credentials(&mut self, org_id: &str) -> Result<Vec<CredentialView>, BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        self.dispatch(async move {
            let resp = client
                .orgs
                .list_credentials(client.auth.request(v1::ListCredentialsRequest { org_id }))
                .await?
                .into_inner();
            Ok(resp
                .credentials
                .into_iter()
                .map(|c| CredentialView { kind: c.kind, name: c.name })
                .collect())
        })
        .await
    }

    /// register_provider registers where environments run. The server probes
    /// the real control plane before storing anything, so a failure here is a
    /// cluster that could not be reached rather than a value that looked wrong.
    pub async fn register_provider(
        &mut self,
        org_id: &str,
        kind: &str,
        name: &str,
        config_json: &str,
    ) -> Result<ProviderView, BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        let kind = kind.to_string();
        let name = name.to_string();
        let config_json = config_json.to_string();
        self.dispatch(async move {
            let provider = client
                .orgs
                .register_provider(client.auth.request(v1::RegisterProviderRequest {
                    org_id,
                    kind,
                    name,
                    config_json,
                }))
                .await?
                .into_inner()
                .provider
                .ok_or_else(|| BoxError::from("register_provider returned no provider"))?;
            Ok(ProviderView::from_proto(&provider))
        })
        .await
    }

    /// list_providers fetches the org's registrations with their capabilities.
    pub async fn list_providers(&mut self, org_id: &str) -> Result<Vec<ProviderView>, BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        self.dispatch(async move {
            let resp = client
                .orgs
                .list_providers(client.auth.request(v1::ListProvidersRequest { org_id }))
                .await?
                .into_inner();
            Ok(resp.providers.iter().map(ProviderView::from_proto).collect())
        })
        .await
    }

    /// delete_provider removes a registration. The server refuses while it
    /// still hosts environments, so the error is the useful part here.
    pub async fn delete_provider(&mut self, org_id: &str, provider_id: &str) -> Result<(), BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        let provider_id = provider_id.to_string();
        self.dispatch(async move {
            client
                .orgs
                .delete_provider(client.auth.request(v1::DeleteProviderRequest { org_id, provider_id }))
                .await?;
            Ok(())
        })
        .await
    }

    /// list_envs reports a session's environments with the registration that
    /// hosts each one, which is what lets the window name where work runs.
    pub async fn list_envs(&mut self, session_id: &str) -> Result<Vec<EnvHostView>, BoxError> {
        let mut client = self.clone();
        let session_id = session_id.to_string();
        self.dispatch(async move {
            let resp = client
                .envs
                .list_envs(client.auth.request(v1::ListEnvsRequest { session_id }))
                .await?
                .into_inner();
            Ok(resp
                .envs
                .into_iter()
                .map(|env| EnvHostView {
                    env_id: env.id,
                    name: env.spec.map(|s| s.name).unwrap_or_default(),
                    provider_name: env.provider_name,
                    provider_kind: env.provider_kind,
                    provider_state: env.provider_state,
                })
                .collect())
        })
        .await
    }

    /// list_flows fetches the org's flow catalog: the latest version of each.
    pub async fn list_flows(&mut self, org_id: &str) -> Result<Vec<FlowSummary>, BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        self.dispatch(async move {
            let resp = client
                .flows
                .list_flows(client.auth.request(v1::ListFlowsRequest { org_id }))
                .await?
                .into_inner();
            Ok(resp
                .flows
                .into_iter()
                .map(|f| FlowSummary {
                    id: f.id,
                    name: f.name,
                    version: f.version,
                    definition: f.definition_json,
                })
                .collect())
        })
        .await
    }

    /// list_flow_versions fetches every version of one flow, newest first, so
    /// selecting an older version shows that version's own definition.
    pub async fn list_flow_versions(&mut self, org_id: &str, name: &str) -> Result<Vec<FlowSummary>, BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        let name = name.to_string();
        self.dispatch(async move {
            let resp = client
                .flows
                .list_flow_versions(client.auth.request(v1::ListFlowVersionsRequest { org_id, name }))
                .await?
                .into_inner();
            Ok(resp
                .flows
                .into_iter()
                .map(|f| FlowSummary {
                    id: f.id,
                    name: f.name,
                    version: f.version,
                    definition: f.definition_json,
                })
                .collect())
        })
        .await
    }

    /// validate_flow reports structured errors without storing anything.
    pub async fn validate_flow(
        &mut self,
        org_id: &str,
        definition_json: &str,
    ) -> Result<Vec<FlowFieldErrorView>, BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        let definition_json = definition_json.to_string();
        self.dispatch(async move {
            let resp = client
                .flows
                .validate_flow(client.auth.request(v1::ValidateFlowRequest { org_id, definition_json }))
                .await?
                .into_inner();
            Ok(resp
                .errors
                .into_iter()
                .map(|e| FlowFieldErrorView { path: e.path, code: e.code, message: e.message })
                .collect())
        })
        .await
    }

    /// put_flow stores a version. A rejected write returns the server's own
    /// field errors rather than a message, so the window shows the same
    /// structured list every other client shows.
    pub async fn put_flow(
        &mut self,
        org_id: &str,
        name: &str,
        definition_json: &str,
    ) -> Result<Result<FlowSummary, Vec<FlowFieldErrorView>>, BoxError> {
        let mut client = self.clone();
        let org_id = org_id.to_string();
        let name = name.to_string();
        let definition_json = definition_json.to_string();
        self.dispatch(async move {
            let request = v1::PutFlowRequest { org_id: org_id.clone(), name, definition_json: definition_json.clone(), version: 0 };
            match client.flows.put_flow(client.auth.request(request)).await {
                Ok(resp) => {
                    let flow = resp
                        .into_inner()
                        .flow
                        .ok_or_else(|| BoxError::from("put_flow returned no flow"))?;
                    Ok(Ok(FlowSummary {
                        id: flow.id,
                        name: flow.name,
                        version: flow.version,
                        definition: flow.definition_json,
                    }))
                }
                Err(status) if status.code() == tonic::Code::InvalidArgument => {
                    // gRPC error details are not carried by tonic's Status in a
                    // typed form here, so the structured list is re-derived
                    // from the same validation surface the write used.
                    let errors = client.validate_flow(&org_id, &definition_json).await?;
                    Ok(Err(errors))
                }
                Err(status) => Err(BoxError::from(status)),
            }
        })
        .await
    }

    /// invoke_flow starts an invocation of the named version.
    pub async fn invoke_flow(
        &mut self,
        session_id: &str,
        name: &str,
        version: i32,
        params_json: &str,
    ) -> Result<String, BoxError> {
        let mut client = self.clone();
        let session_id = session_id.to_string();
        let name = name.to_string();
        let params_json = params_json.to_string();
        self.dispatch(async move {
            let resp = client
                .flows
                .invoke_flow(client.auth.request(v1::InvokeFlowRequest {
                    session_id,
                    name,
                    version,
                    params_json,
                }))
                .await?
                .into_inner();
            Ok(resp.invocation_id)
        })
        .await
    }

    /// list_invocations fetches every invocation in a session with its
    /// progress, runs, and environments. The whole view arrives at once
    /// because assembling it request by request would race the live stream.
    pub async fn list_invocations(&mut self, session_id: &str) -> Result<Vec<FlowInvocationView>, BoxError> {
        let mut client = self.clone();
        let session_id = session_id.to_string();
        self.dispatch(async move {
            let resp = client
                .flows
                .list_flow_invocations(client.auth.request(v1::ListFlowInvocationsRequest { session_id }))
                .await?
                .into_inner();
            Ok(resp.invocations.iter().map(FlowInvocationView::from_proto).collect())
        })
        .await
    }

    /// get_invocation fetches one invocation by its identifier alone. It is
    /// the path an operator follows from a CLI or an alert, and it does not
    /// require the session's invocation list to have been loaded.
    pub async fn get_invocation(&mut self, invocation_id: &str) -> Result<FlowInvocationView, BoxError> {
        let mut client = self.clone();
        let invocation_id = invocation_id.to_string();
        self.dispatch(async move {
            let resp = client
                .flows
                .get_flow_invocation(client.auth.request(v1::GetFlowInvocationRequest { invocation_id }))
                .await?
                .into_inner();
            let invocation = resp
                .invocation
                .ok_or_else(|| BoxError::from("get_flow_invocation returned no invocation"))?;
            Ok(FlowInvocationView::from_proto(&invocation))
        })
        .await
    }

    /// cancel_invocation asks an invocation to converge on cancelled.
    pub async fn cancel_invocation(&mut self, invocation_id: &str) -> Result<(), BoxError> {
        let mut client = self.clone();
        let invocation_id = invocation_id.to_string();
        self.dispatch(async move {
            client
                .flows
                .cancel_flow_invocation(client.auth.request(v1::CancelFlowInvocationRequest { invocation_id }))
                .await?;
            Ok(())
        })
        .await
    }

    pub async fn first_org(&mut self) -> Result<String, BoxError> {
        let mut client = self.clone();
        self.dispatch(async move {
            let orgs = client
                .orgs
                .list_orgs(client.auth.request(v1::ListOrgsRequest {}))
                .await?
                .into_inner();
            orgs.orgs
                .first()
                .map(|org| org.id.clone())
                .ok_or_else(|| BoxError::from("authenticated user belongs to no org"))
        })
        .await
    }

    /// subscribe streams a session's events onto a channel from `from_seq`.
    /// The window pumps that channel on its own executor, which is how a live
    /// stream becomes rendered frames.
    pub async fn subscribe(
        &mut self,
        session_id: &str,
        from_seq: i64,
    ) -> Result<Receiver<StreamMessage>, BoxError> {
        let mut client = self.clone();
        let session_id = session_id.to_string();
        let runtime = self.runtime.clone();
        self.dispatch(async move {
            let mut stream = client
                .events
                .subscribe(client.auth.request(v1::SubscribeRequest { session_id, from_seq }))
                .await?
                .into_inner();
            let (tx, rx): (Sender<StreamMessage>, Receiver<StreamMessage>) = channel();
            tx.send(StreamMessage::Connected).ok();
            runtime.spawn(async move {
                loop {
                    match stream.message().await {
                        Ok(Some(resp)) => {
                            if let Some(event) = resp.event {
                                if tx.send(StreamMessage::Event(Box::new(event))).is_err() {
                                    return;
                                }
                            }
                        }
                        Ok(None) => {
                            tx.send(StreamMessage::Closed).ok();
                            return;
                        }
                        Err(status) => {
                            tx.send(StreamMessage::Failed(status.message().to_string())).ok();
                            return;
                        }
                    }
                }
            });
            Ok(rx)
        })
        .await
    }
}

/// ultra_org_from_env resolves the org the desktop should open: ULTRA_ORG_ID
/// when set, otherwise the first org the authenticated user belongs to.
pub async fn ultra_org_from_env(client: &mut DesktopClient) -> Result<String, BoxError> {
    if let Ok(org_id) = std::env::var("ULTRA_ORG_ID") {
        if !org_id.is_empty() {
            return Ok(org_id);
        }
    }
    client.first_org().await
}

/// StreamMessage is what the subscription pump delivers to the window.
#[derive(Debug)]
pub enum StreamMessage {
    Connected,
    Event(Box<v1::SessionEvent>),
    Closed,
    Failed(String),
}
