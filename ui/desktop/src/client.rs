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

use crate::state::{SessionSummary, UsageView};

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
            billing: v1::billing_service_client::BillingServiceClient::new(channel),
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
