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
    Note { text: String },
}

impl TimelineItem {
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
            TimelineItem::Note { text } => format!("note: {text}"),
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
}

impl DesktopState {
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
        });
    }
}
