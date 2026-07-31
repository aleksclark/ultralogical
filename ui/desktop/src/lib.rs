//! The Ultralogical desktop application.
//!
//! `window` owns the rendered GPUI view, `state` owns the view state derived
//! from the session event log, and `client` owns the generated tonic clients
//! and the actions the window can perform. The native entrypoint and the UI
//! tests share all three: there is no headless-only substitute path.

pub mod client;
pub mod runtime;
pub mod state;
pub mod window;

pub use client::{BoxError, DesktopClient, StreamMessage, ultra_org_from_env};
pub use state::{ConnectionState, DesktopState, EnvView, SessionSummary, TimelineItem, UsageView};
pub use window::{DarkTheme, DesktopWindow, SESSION_WINDOW_SIZE, WindowEntity, build_window};
