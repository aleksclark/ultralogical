//! The Ultralogical desktop window: a real GPUI view with a session list, a
//! session timeline, an environment panel, a usage panel, connection state,
//! and a prompt field.
//!
//! The window is the only renderer of desktop state, and every action it
//! exposes is a method on this view. The native entrypoint in main.rs opens
//! this window; UI tests open the same window in a GPUI test app and dispatch
//! the same methods, then read the rendered element tree. There is no
//! separate test-only rendering or state path.

use std::sync::mpsc::Receiver;
use std::time::Duration;

use gpui::{
    App, Context, Entity, FocusHandle, Focusable, Hsla, KeyDownEvent, MouseButton, Render, Window,
    div, prelude::*, px,
};

use crate::client::{DesktopClient, StreamMessage};
use crate::state::{ConnectionState, DesktopState, SessionSummary, TimelineItem};

/// DarkTheme is the required dark palette. There is no light variant.
pub struct DarkTheme;
impl DarkTheme {
    pub const BACKGROUND: Hsla = Hsla { h: 0.66, s: 0.10, l: 0.04, a: 1.0 };
    pub const SURFACE: Hsla = Hsla { h: 0.66, s: 0.08, l: 0.10, a: 1.0 };
    pub const BORDER: Hsla = Hsla { h: 0.66, s: 0.06, l: 0.18, a: 1.0 };
    pub const TEXT: Hsla = Hsla { h: 0.0, s: 0.0, l: 0.98, a: 1.0 };
    pub const MUTED: Hsla = Hsla { h: 0.0, s: 0.0, l: 0.62, a: 1.0 };
    pub const ACCENT: Hsla = Hsla { h: 0.42, s: 0.55, l: 0.42, a: 1.0 };
    pub const DANGER: Hsla = Hsla { h: 0.02, s: 0.60, l: 0.42, a: 1.0 };
}

/// PUMP_INTERVAL is how often the window drains its subscription channel.
const PUMP_INTERVAL: Duration = Duration::from_millis(10);

/// DesktopWindow is the root GPUI view.
pub struct DesktopWindow {
    pub state: DesktopState,
    client: Option<DesktopClient>,
    stream: Option<Receiver<StreamMessage>>,
    focus: FocusHandle,
}

impl DesktopWindow {
    /// new builds the window view and starts its render pump. The pump is what
    /// turns streamed events into rendered frames, so it runs identically
    /// under the native entrypoint and under UI tests.
    pub fn new(cx: &mut Context<Self>) -> Self {
        cx.spawn(async move |this, cx| {
            loop {
                let alive = this
                    .update(cx, |window: &mut DesktopWindow, cx| {
                        window.drain_stream(cx);
                    })
                    .is_ok();
                if !alive {
                    return;
                }
                cx.background_executor().timer(PUMP_INTERVAL).await;
            }
        })
        .detach();
        Self { state: DesktopState::default(), client: None, stream: None, focus: cx.focus_handle() }
    }

    /// attach binds a connected client and the org the window works in.
    pub fn attach(&mut self, client: DesktopClient, org_id: String, cx: &mut Context<Self>) {
        self.client = Some(client);
        self.state.org_id = org_id;
        cx.notify();
    }

    pub fn client(&self) -> Option<DesktopClient> {
        self.client.clone()
    }

    pub fn set_sessions(&mut self, sessions: Vec<SessionSummary>, cx: &mut Context<Self>) {
        self.state.sessions = sessions;
        cx.notify();
    }

    pub fn set_usage(&mut self, usage: Vec<crate::state::UsageView>, total: i64, cx: &mut Context<Self>) {
        self.state.usage = usage;
        self.state.usage_total_seconds = total;
        cx.notify();
    }

    pub fn set_error(&mut self, message: String, cx: &mut Context<Self>) {
        self.state.error = message;
        cx.notify();
    }

    pub fn set_prompt(&mut self, prompt: String, cx: &mut Context<Self>) {
        self.state.prompt = prompt;
        cx.notify();
    }

    /// open_session switches the window to a session and installs the live
    /// event stream that drives its timeline.
    pub fn open_session(&mut self, session_id: String, stream: Receiver<StreamMessage>, cx: &mut Context<Self>) {
        self.state.reset_session(session_id);
        self.state.connection = ConnectionState::Connecting;
        self.stream = Some(stream);
        cx.notify();
    }

    /// replay_session discards rendered session state and resubscribes from
    /// seq 0, proving the timeline is derived from the log.
    pub fn replay_session(&mut self, stream: Receiver<StreamMessage>, cx: &mut Context<Self>) {
        if let Some(session) = self.state.active_session.clone() {
            self.state.reset_session(session);
        }
        self.state.connection = ConnectionState::Connecting;
        self.stream = Some(stream);
        cx.notify();
    }

    pub fn set_exec_output(&mut self, output: String, cx: &mut Context<Self>) {
        self.state.exec_output = output;
        cx.notify();
    }

    /// start_up is the application's startup sequence: list the org's
    /// sessions, open the first one, and load usage. The native entrypoint
    /// calls it after connecting, and UI tests call the same function, so
    /// there is no test-only startup path that could diverge from what a user
    /// sees when the application launches.
    pub async fn start_up(
        window: &WindowEntity,
        client: &mut DesktopClient,
        org_id: &str,
        cx: &mut gpui::AsyncApp,
    ) {
        let sessions = client.list_sessions(org_id).await.unwrap_or_default();
        let first = sessions.first().cloned();
        let attach = client.clone();
        let org = org_id.to_string();
        let _ = window.update(cx, |view: &mut DesktopWindow, cx| {
            view.attach(attach, org, cx);
            view.set_sessions(sessions, cx);
        });
        if let Some(session) = first {
            if let Ok(stream) = client.subscribe(&session.id, 0).await {
                let _ = window.update(cx, |view: &mut DesktopWindow, cx| {
                    view.open_session(session.id.clone(), stream, cx);
                });
            }
        }
        if let Ok((usage, total)) = client.usage(org_id).await {
            let _ = window.update(cx, |view: &mut DesktopWindow, cx| {
                view.set_usage(usage, total, cx);
            });
        }
    }

    fn drain_stream(&mut self, cx: &mut Context<Self>) {
        let Some(stream) = self.stream.as_ref() else { return };
        let mut changed = false;
        loop {
            match stream.try_recv() {
                Ok(StreamMessage::Connected) => {
                    self.state.connection = ConnectionState::Live;
                    changed = true;
                }
                Ok(StreamMessage::Event(event)) => {
                    self.state.connection = ConnectionState::Live;
                    self.state.fold(&event);
                    changed = true;
                }
                Ok(StreamMessage::Closed) => {
                    self.state.connection = ConnectionState::Disconnected;
                    self.stream = None;
                    changed = true;
                    break;
                }
                Ok(StreamMessage::Failed(message)) => {
                    self.state.connection = ConnectionState::Failed;
                    self.state.error = message;
                    self.stream = None;
                    changed = true;
                    break;
                }
                Err(std::sync::mpsc::TryRecvError::Empty) => break,
                Err(std::sync::mpsc::TryRecvError::Disconnected) => {
                    self.state.connection = ConnectionState::Disconnected;
                    self.stream = None;
                    changed = true;
                    break;
                }
            }
        }
        if changed {
            cx.notify();
        }
    }

    fn on_key(&mut self, event: &KeyDownEvent, cx: &mut Context<Self>) {
        let keystroke = &event.keystroke;
        if keystroke.key == "backspace" {
            self.state.prompt.pop();
        } else if let Some(text) = keystroke.key_char.as_ref() {
            self.state.prompt.push_str(text);
        }
        cx.notify();
    }
}

impl Focusable for DesktopWindow {
    fn focus_handle(&self, _: &App) -> FocusHandle {
        self.focus.clone()
    }
}

impl Render for DesktopWindow {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        window.focus(&self.focus);
        div()
            .track_focus(&self.focus)
            .on_key_down(cx.listener(|this, event: &KeyDownEvent, _, cx| this.on_key(event, cx)))
            .flex()
            .size_full()
            .bg(DarkTheme::BACKGROUND)
            .text_color(DarkTheme::TEXT)
            .debug_selector(|| "window:dark".to_string())
            .child(self.render_sidebar())
            .child(self.render_main(cx))
    }
}

impl DesktopWindow {
    fn render_sidebar(&self) -> impl IntoElement {
        let connection = self.state.connection.label();
        div()
            .w(px(240.0))
            .h_full()
            .flex()
            .flex_col()
            .gap_2()
            .p_3()
            .bg(DarkTheme::SURFACE)
            .border_r_1()
            .border_color(DarkTheme::BORDER)
            .debug_selector(|| "sidebar".to_string())
            .child(div().child("Ultralogical").debug_selector(|| "title".to_string()))
            .child(
                div()
                    .text_color(match self.state.connection {
                        ConnectionState::Live => DarkTheme::ACCENT,
                        ConnectionState::Failed => DarkTheme::DANGER,
                        _ => DarkTheme::MUTED,
                    })
                    .child(format!("connection: {connection}"))
                    .debug_selector(move || format!("connection:{connection}")),
            )
            .child(
                div()
                    .flex()
                    .flex_col()
                    .gap_1()
                    .debug_selector(|| "session-list".to_string())
                    .children(self.state.sessions.iter().map(|session| {
                        let active = self.state.active_session.as_deref() == Some(session.id.as_str());
                        let title = session.title.clone();
                        div()
                            .px_2()
                            .py_1()
                            .when(active, |el| el.bg(DarkTheme::BORDER))
                            .child(session.title.clone())
                            .debug_selector(move || format!("session:{title}"))
                    })),
            )
    }

    fn render_main(&self, cx: &mut Context<Self>) -> impl IntoElement {
        div()
            .flex_1()
            .h_full()
            .flex()
            .flex_col()
            .gap_2()
            .p_3()
            .debug_selector(|| "main".to_string())
            .child(self.render_environments(cx))
            .child(self.render_usage())
            .child(self.render_memory())
            .child(self.render_timeline())
            .child(self.render_prompt())
            .when(!self.state.error.is_empty(), |el| {
                let error = self.state.error.clone();
                el.child(
                    div()
                        .text_color(DarkTheme::DANGER)
                        .child(self.state.error.clone())
                        .debug_selector(move || format!("error:{error}")),
                )
            })
    }

    fn render_environments(&self, cx: &mut Context<Self>) -> impl IntoElement {
        div()
            .flex()
            .flex_col()
            .gap_1()
            .debug_selector(|| "environment-panel".to_string())
            .child(div().child("Environments").text_color(DarkTheme::MUTED))
            .children(self.state.envs.values().map(|env| {
                let label = format!("env:{}:{}:{}", env.name, env.phase, env.epoch);
                let env_id = env.env_id.clone();
                div()
                    .flex()
                    .gap_2()
                    .child(div().child(format!("{}: {} (epoch {})", env.name, env.phase, env.epoch)))
                    .child(
                        div()
                            .id("restart")
                            .w(px(80.0))
                            .h(px(24.0))
                            .bg(DarkTheme::SURFACE)
                            .child("Restart")
                            .debug_selector({
                                let name = env.name.clone();
                                move || format!("restart:{name}")
                            })
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |this, _, _, cx| {
                                    this.request_restart(env_id.clone(), cx);
                                }),
                            ),
                    )
                    .debug_selector(move || label.clone())
            }))
            .when(!self.state.exec_output.is_empty(), |el| {
                let output = self.state.exec_output.trim().to_string();
                el.child(
                    div()
                        .child(self.state.exec_output.clone())
                        .debug_selector(move || format!("exec-output:{output}")),
                )
            })
    }

    fn render_usage(&self) -> impl IntoElement {
        let total = self.state.usage_total_seconds;
        div()
            .flex()
            .flex_col()
            .gap_1()
            .debug_selector(|| "usage-panel".to_string())
            .child(
                div()
                    .child(format!("usage total: {total}s"))
                    .debug_selector(move || format!("usage-total:{total}")),
            )
            .children(self.state.usage.iter().map(|interval| {
                let label = format!(
                    "usage:{}:{}:{}",
                    interval.env_id,
                    if interval.open { "open" } else { "closed" },
                    interval.seconds
                );
                div()
                    .child(format!(
                        "{} {} {}s {}",
                        &interval.env_id[..interval.env_id.len().min(8)],
                        interval.rate_class,
                        interval.seconds,
                        if interval.open { "open" } else { "closed" }
                    ))
                    .debug_selector(move || label.clone())
            }))
    }

    /// render_memory paints the session's memory keys. Memory is folded from
    /// the event log, so a painted key proves the window observed the write
    /// rather than echoing its own request.
    fn render_memory(&self) -> impl IntoElement {
        let count = self.state.memory_keys.len();
        div()
            .flex()
            .flex_col()
            .gap_1()
            .debug_selector(|| "memory-panel".to_string())
            .child(
                div()
                    .text_color(DarkTheme::MUTED)
                    .child(format!("session memory: {count} entries"))
                    .debug_selector(move || format!("memory-count:{count}")),
            )
            .children(self.state.memory_keys.iter().map(|key| {
                let label = format!("memory:{key}");
                div().child(key.clone()).debug_selector(move || label.clone())
            }))
    }

    fn render_timeline(&self) -> impl IntoElement {
        let frames = self.state.delta_frames;
        div()
            .flex_1()
            .flex()
            .flex_col()
            .gap_1()
            .overflow_hidden()
            .debug_selector(|| "timeline".to_string())
            .child(
                div()
                    .text_color(DarkTheme::MUTED)
                    .child(format!("streamed frames: {frames}"))
                    .debug_selector(move || format!("delta-frames:{frames}")),
            )
            .children(self.state.timeline.iter().map(|item| {
                let label = item.render_label();
                let selector = label.clone();
                div()
                    .text_color(match item {
                        TimelineItem::Note { .. } => DarkTheme::MUTED,
                        _ => DarkTheme::TEXT,
                    })
                    .child(label)
                    .debug_selector(move || format!("row:{selector}"))
            }))
    }

    fn render_prompt(&self) -> impl IntoElement {
        let prompt = self.state.prompt.clone();
        div()
            .h(px(32.0))
            .px_2()
            .bg(DarkTheme::SURFACE)
            .border_1()
            .border_color(DarkTheme::BORDER)
            .child(if prompt.is_empty() { "Ask an agent…".to_string() } else { prompt.clone() })
            .debug_selector(move || format!("prompt:{prompt}"))
    }

    /// request_restart is the window's restart action, invoked by the rendered
    /// control and by tests through the same entity update.
    pub fn request_restart(&mut self, env_id: String, cx: &mut Context<Self>) {
        let Some(mut client) = self.client.clone() else { return };
        cx.spawn(async move |this, cx| {
            let result = client.restart_env(&env_id).await;
            let _ = this.update(cx, |window: &mut DesktopWindow, cx| {
                if let Err(err) = result {
                    window.set_error(err.to_string(), cx);
                }
            });
        })
        .detach();
    }
}

/// SESSION_WINDOW_SIZE is the default window size for the native entrypoint.
pub const SESSION_WINDOW_SIZE: (f32, f32) = (1100.0, 760.0);

/// build_window creates the root view. Both the native entrypoint and the UI
/// tests call it, so they render the same tree.
pub fn build_window(cx: &mut Context<DesktopWindow>) -> DesktopWindow {
    DesktopWindow::new(cx)
}

/// WindowEntity is the handle callers hold onto after opening the window.
pub type WindowEntity = Entity<DesktopWindow>;
