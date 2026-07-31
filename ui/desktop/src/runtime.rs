//! The Tokio runtime the desktop uses for network work.
//!
//! GPUI owns the UI executor; tonic needs a Tokio reactor. The application
//! keeps one multi-threaded runtime alive for the process lifetime and hands
//! its handle to the client, so no request ever runs on the render thread.

use std::sync::OnceLock;
use tokio::runtime::{Handle, Runtime};

static RUNTIME: OnceLock<Runtime> = OnceLock::new();

/// handle returns the process-wide network runtime, creating it on first use.
pub fn handle() -> Handle {
    RUNTIME
        .get_or_init(|| {
            tokio::runtime::Builder::new_multi_thread()
                .worker_threads(2)
                .enable_all()
                .thread_name("ultralogical-net")
                .build()
                .expect("build desktop network runtime")
        })
        .handle()
        .clone()
}
