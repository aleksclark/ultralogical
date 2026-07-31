use gpui::Hsla;
use ultralogical_client::{Auth, ultra::v1};
use tonic::transport::Channel;

pub struct DarkTheme;
impl DarkTheme {
    pub const BACKGROUND: Hsla = Hsla { h: 0.66, s: 0.10, l: 0.04, a: 1.0 };
    pub const SURFACE: Hsla = Hsla { h: 0.66, s: 0.08, l: 0.10, a: 1.0 };
    pub const TEXT: Hsla = Hsla { h: 0.0, s: 0.0, l: 0.98, a: 1.0 };
}

#[derive(Default, Debug)]
pub struct GpuiDesktopState { pub core: DesktopState, pub active_panel: String, pub provider_kinds: Vec<String> }
impl GpuiDesktopState { pub fn dark_theme(&self) -> Hsla { DarkTheme::BACKGROUND } }

pub struct DesktopClient {
    auth: Auth,
    pub orgs: v1::org_service_client::OrgServiceClient<Channel>,
    pub sessions: v1::session_service_client::SessionServiceClient<Channel>,
    pub events: v1::event_service_client::EventServiceClient<Channel>,
    pub agents: v1::agent_service_client::AgentServiceClient<Channel>,
    pub envs: v1::env_service_client::EnvServiceClient<Channel>,
    pub billing: v1::billing_service_client::BillingServiceClient<Channel>,
}

impl DesktopClient {
    pub async fn connect(url: String, token: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let channel = Channel::from_shared(url)?.connect().await?;
        Ok(Self {
            auth: Auth::new(token)?,
            orgs: v1::org_service_client::OrgServiceClient::new(channel.clone()),
            sessions: v1::session_service_client::SessionServiceClient::new(channel.clone()),
            events: v1::event_service_client::EventServiceClient::new(channel.clone()),
            agents: v1::agent_service_client::AgentServiceClient::new(channel.clone()),
            envs: v1::env_service_client::EnvServiceClient::new(channel.clone()),
            billing: v1::billing_service_client::BillingServiceClient::new(channel),
        })
    }

    pub fn auth<T>(&self, value: T) -> tonic::Request<T> { self.auth.request(value) }
}

#[derive(Default, Debug)]
pub struct DesktopState {
    pub last_seq: i64,
    pub messages: Vec<String>,
    pub run_states: Vec<String>,
    pub environments: Vec<String>,
    pub participants: Vec<String>,
    pub memory_keys: Vec<String>,
}

impl DesktopState {
    pub fn fold(&mut self, event: &v1::SessionEvent) {
        if event.seq <= self.last_seq { return; }
        self.last_seq = event.seq;
        let Some(payload) = &event.payload else { return };
        use v1::event_payload::Payload;
        match payload.payload.as_ref() {
            Some(Payload::UserMessage(x)) => self.messages.push(x.text.clone()),
            Some(Payload::TextDelta(x)) => self.messages.push(x.text.clone()),
            Some(Payload::RunCompleted(_)) => self.run_states.push("completed".into()),
            Some(Payload::RunFailed(_)) => self.run_states.push("failed".into()),
            Some(Payload::EnvReady(x)) => self.environments.push(x.name.clone()),
            Some(Payload::ParticipantJoined(x)) => self.participants.push(x.display.clone()),
            Some(Payload::MemorySet(x)) => self.memory_keys.push(x.key.clone()),
            _ => {}
        }
    }
}
