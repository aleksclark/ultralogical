//! Desktop view state. The window renders exclusively from this structure,
//! and the structure is derived exclusively from the session event log plus
//! explicit API reads, so a rendered frame is always reproducible by replay.

use std::collections::BTreeMap;
use ultralogical_client::ultra::v1;

/// ConnectionState is what the window shows about its link to ultrad.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum ConnectionState {
    #[default]
    Disconnected,
    Connecting,
    Live,
    Failed,
}

impl ConnectionState {
    pub fn label(self) -> &'static str {
        match self {
            ConnectionState::Disconnected => "disconnected",
            ConnectionState::Connecting => "connecting",
            ConnectionState::Live => "live",
            ConnectionState::Failed => "failed",
        }
    }
}

/// TimelineItem is one rendered row of session history.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum TimelineItem {
    User { text: String },
    Assistant { run_id: String, text: String, streaming: bool },
    Tool { run_id: String, name: String, output: Option<String>, is_error: bool },
    Question { run_id: String, text: String, choices: Vec<String> },
    Status { run_id: String, status: String, message: String },
    Note { text: String, run_id: Option<String> },
}

/// short_id abbreviates an identifier for display.
fn short_id(id: &str) -> String {
    id.chars().take(8).collect()
}

/// run_state_label maps the wire enum to the word the window paints.
pub fn run_state_label(state: i32) -> String {
    match v1::RunState::try_from(state) {
        Ok(v1::RunState::Pending) => "pending",
        Ok(v1::RunState::Running) => "running",
        Ok(v1::RunState::Awaiting) => "awaiting",
        Ok(v1::RunState::Completed) => "completed",
        Ok(v1::RunState::Failed) => "failed",
        Ok(v1::RunState::Cancelled) => "cancelled",
        _ => "unknown",
    }
    .to_string()
}

impl TimelineItem {
    /// run_id reports which run a row belongs to, so lanes can filter.
    pub fn run_id(&self) -> Option<&str> {
        match self {
            TimelineItem::Assistant { run_id, .. }
            | TimelineItem::Tool { run_id, .. }
            | TimelineItem::Question { run_id, .. }
            | TimelineItem::Status { run_id, .. } => Some(run_id),
            TimelineItem::Note { run_id, .. } => run_id.as_deref(),
            TimelineItem::User { .. } => None,
        }
    }

    /// render_label is the text the window paints for this row. Tests assert
    /// against it through the rendered element tree, never against internals.
    pub fn render_label(&self) -> String {
        match self {
            TimelineItem::User { text } => format!("you: {text}"),
            TimelineItem::Assistant { text, streaming, .. } => {
                if *streaming { format!("agent: {text}\u{258d}") } else { format!("agent: {text}") }
            }
            TimelineItem::Tool { name, output, is_error, .. } => match output {
                Some(out) if *is_error => format!("tool {name} failed: {out}"),
                Some(out) => format!("tool {name}: {out}"),
                None => format!("tool {name}: running"),
            },
            TimelineItem::Question { text, choices, .. } => {
                format!("question: {text} [{}]", choices.join("/"))
            }
            TimelineItem::Status { status, message, .. } => {
                if message.is_empty() { format!("run {status}") } else { format!("run {status}: {message}") }
            }
            TimelineItem::Note { text, .. } => format!("note: {text}"),
        }
    }
}

/// EnvView is the rendered lifecycle state of one environment.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct EnvView {
    pub env_id: String,
    pub name: String,
    pub phase: String,
    pub epoch: i32,
    pub message: String,
}

/// UsageView is one rendered metering interval.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct UsageView {
    pub env_id: String,
    pub seconds: i64,
    pub rate_class: String,
    pub open: bool,
}

/// RunNode is one run in the rendered spawn tree, flattened with its depth so
/// the window can paint indentation without recursive layout.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct RunNode {
    pub run_id: String,
    pub parent_run_id: String,
    pub prompt: String,
    pub state: String,
    pub depth: usize,
    pub cohort_id: String,
    pub cohort_ordinal: i32,
    pub waits: Vec<WaitView>,
}

/// WaitView is a rendered fan-in wait: why a run parked and how it ended.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct WaitView {
    pub wait_id: String,
    pub kind: String,
    pub state: String,
    pub member_count: usize,
}

/// CredentialView is one rendered inference credential. Only its identity is
/// ever rendered: the secret is write-only, and a client that could display it
/// would be a place for it to leak.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct CredentialView {
    pub kind: String,
    pub name: String,
}

/// ProviderView is one rendered provider registration: where environments run,
/// how it is metered, and what its control plane reported it can do.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ProviderView {
    pub id: String,
    pub name: String,
    pub kind: String,
    pub rate_class: String,
    pub state: String,
    /// capabilities lists every optional behavior with whether this
    /// registration has it, and why not when it does not. Showing only what
    /// works would leave an operator unable to explain a refusal.
    pub capabilities: Vec<ProviderCapabilityView>,
}

/// ProviderCapabilityView is one capability as the window paints it.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ProviderCapabilityView {
    pub name: String,
    pub supported: bool,
    pub reason: String,
}

impl ProviderView {
    /// from_proto converts the API's registration into rendered state.
    pub fn from_proto(provider: &v1::ProviderInstance) -> Self {
        Self {
            id: provider.id.clone(),
            name: provider.name.clone(),
            kind: provider.kind.clone(),
            rate_class: provider.rate_class.clone(),
            state: provider.state.clone(),
            capabilities: provider
                .capabilities
                .iter()
                .map(|c| ProviderCapabilityView {
                    name: c.name.clone(),
                    supported: c.supported,
                    reason: c.reason.clone(),
                })
                .collect(),
        }
    }

    /// supports reports whether a named capability was confirmed.
    pub fn supports(&self, name: &str) -> bool {
        self.capabilities.iter().any(|c| c.name == name && c.supported)
    }
}

/// FlowSummary is one row of the rendered flow catalog.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct FlowSummary {
    pub id: String,
    pub name: String,
    pub version: i32,
    /// definition is the version's own stored text. Carrying it means
    /// selecting a version shows what that version says, not what the latest
    /// one says.
    pub definition: String,
}

/// FlowFieldErrorView is one rendered validation failure. The path and code
/// come from the server unchanged, so the desktop shows the same structured
/// errors the API produced rather than a reworded approximation.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct FlowFieldErrorView {
    pub path: String,
    pub code: String,
    pub message: String,
}

/// FlowProgressView is one rendered lifecycle step of an invocation.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct FlowProgressView {
    pub seq: i64,
    pub stage: String,
    pub key: String,
    pub detail: String,
}

/// FlowResourceView is one run or environment an invocation owns, rendered
/// with the flow declaration that produced it.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct FlowResourceView {
    pub id: String,
    pub declaration: String,
    pub state: String,
}

/// FlowInvocationView is the rendered state of one invocation: provenance,
/// ordered progress, and the topology it produced.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct FlowInvocationView {
    pub id: String,
    pub flow_id: String,
    pub flow_name: String,
    pub flow_version: i32,
    pub state: String,
    pub terminal_reason: String,
    pub progress: Vec<FlowProgressView>,
    pub runs: Vec<FlowResourceView>,
    pub envs: Vec<FlowResourceView>,
}

/// flow_invocation_state_label maps the wire enum to the word the window
/// paints, matching the API and the web application exactly.
pub fn flow_invocation_state_label(state: i32) -> String {
    match v1::FlowInvocationState::try_from(state) {
        Ok(v1::FlowInvocationState::Pending) => "pending",
        Ok(v1::FlowInvocationState::Provisioning) => "provisioning",
        Ok(v1::FlowInvocationState::Running) => "running",
        Ok(v1::FlowInvocationState::Cancelling) => "cancelling",
        Ok(v1::FlowInvocationState::Completed) => "completed",
        Ok(v1::FlowInvocationState::Failed) => "failed",
        Ok(v1::FlowInvocationState::Cancelled) => "cancelled",
        _ => "unknown",
    }
    .to_string()
}

/// env_state_label maps the environment wire enum to a rendered word.
pub fn env_state_label(state: i32) -> String {
    match v1::EnvState::try_from(state) {
        Ok(v1::EnvState::Requested) => "requested",
        Ok(v1::EnvState::Provisioning) => "provisioning",
        Ok(v1::EnvState::Ready) => "ready",
        Ok(v1::EnvState::Suspended) => "suspended",
        Ok(v1::EnvState::Terminating) => "terminating",
        Ok(v1::EnvState::Terminated) => "terminated",
        Ok(v1::EnvState::Failed) => "failed",
        _ => "unknown",
    }
    .to_string()
}

impl FlowInvocationView {
    /// from_proto converts the API's invocation into rendered state.
    pub fn from_proto(inv: &v1::FlowInvocation) -> Self {
        Self {
            id: inv.id.clone(),
            flow_id: inv.flow_id.clone(),
            flow_name: inv.flow_name.clone(),
            flow_version: inv.flow_version,
            state: flow_invocation_state_label(inv.state),
            terminal_reason: inv.terminal_reason.clone(),
            progress: inv
                .progress
                .iter()
                .map(|p| FlowProgressView {
                    seq: p.seq,
                    stage: p.stage.clone(),
                    key: p.key.clone(),
                    detail: p.detail.clone(),
                })
                .collect(),
            runs: inv
                .runs
                .iter()
                .map(|r| FlowResourceView {
                    id: r.run_id.clone(),
                    declaration: r.agent_name.clone(),
                    state: run_state_label(r.state),
                })
                .collect(),
            envs: inv
                .envs
                .iter()
                .map(|e| FlowResourceView {
                    id: e.env_id.clone(),
                    declaration: e.env_name.clone(),
                    state: env_state_label(e.state),
                })
                .collect(),
        }
    }

    /// progress_keys is the ordered key sequence, which is what replay
    /// equality is asserted against.
    pub fn progress_keys(&self) -> Vec<String> {
        self.progress.iter().map(|p| p.key.clone()).collect()
    }
}

/// SessionSummary is one row of the rendered session list.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SessionSummary {
    pub id: String,
    pub title: String,
}

/// DesktopState is the complete rendered state of the desktop window.
#[derive(Default, Debug)]
pub struct DesktopState {
    pub connection: ConnectionState,
    pub org_id: String,
    pub sessions: Vec<SessionSummary>,
    pub active_session: Option<String>,
    pub timeline: Vec<TimelineItem>,
    pub last_seq: i64,
    /// delta_frames counts folded streamed text deltas. Incremental-rendering
    /// evidence asserts it advances past one before the run completes.
    pub delta_frames: usize,
    pub envs: BTreeMap<String, EnvView>,
    pub usage: Vec<UsageView>,
    pub usage_total_seconds: i64,
    pub exec_output: String,
    pub participants: Vec<String>,
    pub memory_keys: Vec<String>,
    pub error: String,
    pub prompt: String,
    /// runs is the flattened spawn tree. A session runs several agents at once,
    /// so "which agent did this" is unanswerable without it.
    pub runs: Vec<RunNode>,
    /// lane_run_id filters the timeline to one agent's activity.
    pub lane_run_id: Option<String>,
    /// endpoint records which replica the window is currently talking to.
    pub endpoint: String,
    /// flows is the rendered catalog: the latest version of each org flow.
    pub flows: Vec<FlowSummary>,
    /// flow_versions is every version of the selected flow, newest first.
    pub flow_versions: Vec<FlowSummary>,
    pub selected_flow: Option<String>,
    pub selected_version: i32,
    /// flow_definition is the definition text of the selected version.
    pub flow_definition: String,
    /// flow_errors are the server's structured validation failures.
    pub flow_errors: Vec<FlowFieldErrorView>,
    /// invocations are the session's flow invocations, newest first.
    pub invocations: Vec<FlowInvocationView>,
    pub active_invocation: Option<String>,
    /// providers are the org's registrations, as the settings surface shows
    /// them.
    pub providers: Vec<ProviderView>,
    /// credentials are the org's inference credentials, by identity only.
    pub credentials: Vec<CredentialView>,
}

impl DesktopState {
    /// set_runs replaces the rendered spawn tree.
    pub fn set_runs(&mut self, runs: Vec<RunNode>) {
        self.runs = runs;
    }

    /// flatten_tree converts the API's nested tree into rendered rows, carrying
    /// depth so parent/child structure survives flattening.
    pub fn flatten_tree(roots: &[v1::RunTreeNode]) -> Vec<RunNode> {
        fn walk(node: &v1::RunTreeNode, depth: usize, out: &mut Vec<RunNode>) {
            let Some(run) = &node.run else { return };
            out.push(RunNode {
                run_id: run.id.clone(),
                parent_run_id: run.parent_run_id.clone(),
                prompt: run.prompt.clone(),
                state: run_state_label(run.state),
                depth,
                cohort_id: run.cohort_id.clone(),
                cohort_ordinal: run.cohort_ordinal,
                waits: node
                    .waits
                    .iter()
                    .map(|w| WaitView {
                        wait_id: w.id.clone(),
                        kind: w.kind.clone(),
                        state: w.state.clone(),
                        member_count: w.member_run_ids.len(),
                    })
                    .collect(),
            });
            for child in &node.children {
                walk(child, depth + 1, out);
            }
        }
        let mut out = Vec::new();
        for root in roots {
            walk(root, 0, &mut out);
        }
        out
    }

    /// timeline_for returns the rows a lane shows: one agent's activity when a
    /// lane is selected, everything otherwise.
    pub fn timeline_for(&self, lane: Option<&str>) -> Vec<&TimelineItem> {
        self.timeline
            .iter()
            .filter(|item| match lane {
                None => true,
                Some(run) => item.run_id().is_some_and(|id| id == run),
            })
            .collect()
    }

    /// reset_session clears per-session state when the window switches
    /// sessions or replays one from seq 0.
    pub fn reset_session(&mut self, session_id: String) {
        self.active_session = Some(session_id);
        self.timeline.clear();
        self.last_seq = 0;
        self.delta_frames = 0;
        self.envs.clear();
        self.exec_output.clear();
        self.participants.clear();
        self.memory_keys.clear();
        self.runs.clear();
        self.lane_run_id = None;
        self.invocations.clear();
        self.active_invocation = None;
    }

    /// active_invocation_view returns the invocation the window is showing.
    pub fn active_invocation_view(&self) -> Option<&FlowInvocationView> {
        match self.active_invocation.as_deref() {
            Some(id) => self.invocations.iter().find(|inv| inv.id == id),
            None => self.invocations.first(),
        }
    }

    /// set_flow_catalog replaces the rendered catalog.
    pub fn set_flow_catalog(&mut self, flows: Vec<FlowSummary>) {
        self.flows = flows;
    }

    /// select_flow_version shows one version's definition, which is how a user
    /// inspects an older version rather than always seeing the latest.
    pub fn select_flow_version(&mut self, version: i32) {
        self.selected_version = version;
        if let Some(found) = self.flow_versions.iter().find(|f| f.version == version) {
            self.selected_flow = Some(found.name.clone());
            self.flow_definition = found.definition.clone();
        }
    }

    /// assistant_text returns the concatenated assistant text, used by the
    /// window and by replay-equality evidence.
    pub fn assistant_text(&self) -> String {
        self.timeline
            .iter()
            .filter_map(|item| match item {
                TimelineItem::Assistant { text, .. } => Some(text.clone()),
                _ => None,
            })
            .collect::<Vec<_>>()
            .join("\n")
    }

    /// env_by_name finds a rendered environment by its spec name.
    pub fn env_by_name(&self, name: &str) -> Option<&EnvView> {
        self.envs.values().find(|env| env.name == name)
    }

    /// fold applies one session event. Out-of-order and duplicate deliveries
    /// are ignored, so the rendered timeline is gapless and idempotent.
    pub fn fold(&mut self, event: &v1::SessionEvent) {
        if event.seq <= self.last_seq {
            return;
        }
        self.last_seq = event.seq;
        let Some(payload) = &event.payload else { return };
        use v1::event_payload::Payload;
        match payload.payload.as_ref() {
            Some(Payload::UserMessage(x)) => self.timeline.push(TimelineItem::User { text: x.text.clone() }),
            Some(Payload::RunStarted(x)) => self.timeline.push(TimelineItem::Status {
                run_id: x.run_id.clone(),
                status: "running".into(),
                message: String::new(),
            }),
            Some(Payload::TextDelta(x)) => {
                self.delta_frames += 1;
                match self.timeline.iter_mut().rev().find(|item| {
                    matches!(item, TimelineItem::Assistant { run_id, streaming, .. } if *streaming && run_id == &x.run_id)
                }) {
                    Some(TimelineItem::Assistant { text, .. }) => text.push_str(&x.text),
                    _ => self.timeline.push(TimelineItem::Assistant {
                        run_id: x.run_id.clone(),
                        text: x.text.clone(),
                        streaming: true,
                    }),
                }
            }
            Some(Payload::ToolCallStarted(x)) => self.timeline.push(TimelineItem::Tool {
                run_id: x.run_id.clone(),
                name: x.name.clone(),
                output: None,
                is_error: false,
            }),
            Some(Payload::ToolResult(x)) => {
                match self.timeline.iter_mut().rev().find(|item| {
                    matches!(item, TimelineItem::Tool { run_id, name, output, .. }
                        if output.is_none() && run_id == &x.run_id && name == &x.name)
                }) {
                    Some(TimelineItem::Tool { output, is_error, .. }) => {
                        *output = Some(x.content.clone());
                        *is_error = x.is_error;
                    }
                    _ => self.timeline.push(TimelineItem::Tool {
                        run_id: x.run_id.clone(),
                        name: x.name.clone(),
                        output: Some(x.content.clone()),
                        is_error: x.is_error,
                    }),
                }
            }
            Some(Payload::RunAwaiting(x)) => self.timeline.push(TimelineItem::Question {
                run_id: x.run_id.clone(),
                text: x.question.as_ref().map(|q| q.text.clone()).unwrap_or_default(),
                choices: x.question.as_ref().map(|q| q.choices.clone()).unwrap_or_default(),
            }),
            Some(Payload::RunCompleted(x)) => {
                self.finish_streaming(&x.run_id);
                self.timeline.push(TimelineItem::Status {
                    run_id: x.run_id.clone(),
                    status: "completed".into(),
                    message: String::new(),
                });
            }
            Some(Payload::RunFailed(x)) => {
                self.finish_streaming(&x.run_id);
                self.timeline.push(TimelineItem::Status {
                    run_id: x.run_id.clone(),
                    status: "failed".into(),
                    message: x.message.clone(),
                });
            }
            Some(Payload::RunCancelled(x)) => {
                self.finish_streaming(&x.run_id);
                self.timeline.push(TimelineItem::Status {
                    run_id: x.run_id.clone(),
                    status: "cancelled".into(),
                    message: String::new(),
                });
            }
            Some(Payload::EnvRequested(x)) => self.fold_env(x, "requested"),
            Some(Payload::EnvProvisioning(x)) => self.fold_env(x, "provisioning"),
            Some(Payload::EnvReady(x)) => self.fold_env(x, "ready"),
            Some(Payload::EnvFailed(x)) => self.fold_env(x, "failed"),
            Some(Payload::EnvTerminating(x)) => self.fold_env(x, "terminating"),
            Some(Payload::EnvTerminated(x)) => self.fold_env(x, "terminated"),
            Some(Payload::ExecPreviewRan(x)) => {
                self.exec_output = x.output.clone();
                self.timeline.push(TimelineItem::Tool {
                    run_id: "human".into(),
                    name: format!("exec: {}", x.command),
                    output: Some(x.output.clone()),
                    is_error: x.is_error,
                });
            }
            Some(Payload::ParticipantJoined(x)) => {
                if !self.participants.contains(&x.display) {
                    self.participants.push(x.display.clone());
                }
            }
            Some(Payload::ParticipantLeft(x)) => self.participants.retain(|p| p != &x.display),
            Some(Payload::MemorySet(x)) => {
                if !self.memory_keys.contains(&x.key) {
                    self.memory_keys.push(x.key.clone());
                }
            }
            Some(Payload::MemoryDeleted(x)) => self.memory_keys.retain(|k| k != &x.key),
            Some(Payload::PermissionDenied(x)) => self.timeline.push(TimelineItem::Note {
                text: format!("permission denied: {} ({})", x.tool, x.reason),
                run_id: Some(x.run_id.clone()),
            }),
            // Spawn links are folded so a lane knows about its children even
            // before the run tree is fetched.
            Some(Payload::RunSpawned(x)) => self.timeline.push(TimelineItem::Note {
                text: format!("spawned agent {}", short_id(&x.child_run_id)),
                run_id: Some(x.parent_run_id.clone()),
            }),
            _ => {}
        }
    }

    fn finish_streaming(&mut self, run_id: &str) {
        for item in self.timeline.iter_mut() {
            if let TimelineItem::Assistant { run_id: id, streaming, .. } = item {
                if id == run_id {
                    *streaming = false;
                }
            }
        }
    }

    fn fold_env(&mut self, event: &v1::EnvLifecycle, phase: &str) {
        self.envs.insert(
            event.env_id.clone(),
            EnvView {
                env_id: event.env_id.clone(),
                name: event.name.clone(),
                phase: phase.to_string(),
                epoch: event.epoch,
                message: event.message.clone(),
            },
        );
        self.timeline.push(TimelineItem::Note {
            text: format!("environment {} is {phase}", event.name),
            run_id: None,
        });
    }
}
