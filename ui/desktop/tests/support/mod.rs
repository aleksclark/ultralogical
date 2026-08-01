//! Shared scaffolding for the GPUI application-path tests.
//!
//! Every test here opens the real `DesktopWindow` in a GPUI test app, drives
//! it through the same actions the native entrypoint uses, and inspects the
//! rendered element tree via `debug_bounds`. Nothing asserts against a
//! headless reducer or a direct RPC result alone: a claim is only satisfied
//! when the window actually painted it.

use gpui::{TestAppContext, VisualTestContext};
use std::time::Duration;
use ultralogical_desktop::{DesktopClient, DesktopWindow, WindowEntity, build_window, runtime};

pub fn env(name: &str) -> String {
    std::env::var(name).unwrap_or_else(|_| panic!("{name} must be set by the Go harness"))
}

/// selector leaks a String because GPUI's debug-bounds lookup takes a
/// 'static key. Test processes are short-lived, so this is bounded.
pub fn selector(value: impl Into<String>) -> &'static str {
    Box::leak(value.into().into_boxed_str())
}

/// open_app opens the real window and connects it to the harness stack.
///
/// GPUI's test executor is deterministic and refuses to park by default; these
/// tests deliberately wait on a real server, so parking is enabled.
pub async fn open_app(
    cx: &mut TestAppContext,
) -> (WindowEntity, VisualTestContext, DesktopClient, String) {
    open_app_at(cx, &env("ULTRAD_URL")).await
}

/// open_app_at opens the window against a specific replica, which is how the
/// reconnect test proves state is rebuilt from the log rather than carried in
/// one server's memory.
pub async fn open_app_at(
    cx: &mut TestAppContext,
    endpoint: &str,
) -> (WindowEntity, VisualTestContext, DesktopClient, String) {
    cx.executor().allow_parking();
    let client = DesktopClient::connect(runtime::handle(), endpoint.to_string(), &env("ULTRA_TOKEN"))
        .await
        .expect("connect to ultrad");
    let org_id = env("ULTRA_ORG_ID");
    let (window, visual) = cx.add_window_view(|_, cx| build_window(cx));
    let attach = client.clone();
    let attach_org = org_id.clone();
    let attach_endpoint = endpoint.to_string();
    window.update(visual, |view: &mut DesktopWindow, cx| {
        view.attach(attach, attach_org, attach_endpoint, cx);
    });
    visual.run_until_parked();
    let visual = visual.clone();
    (window, visual, client, org_id)
}

/// PUMP_STEP is how long each pump iteration waits for the real backend.
const PUMP_STEP: Duration = Duration::from_millis(20);

/// pump lets the real stack make progress, then advances the GPUI clock so the
/// window's subscription pump runs and the window repaints. Both halves are
/// required: the wall-clock sleep is what gives ultrad and the worker time to
/// emit events, and the clock advance is what fires the window's timer.
pub fn pump(cx: &mut VisualTestContext) {
    cx.run_until_parked();
    std::thread::sleep(PUMP_STEP);
    cx.executor().advance_clock(PUMP_STEP);
    cx.run_until_parked();
}

/// await_rendered polls the rendered element tree for a selector, pumping the
/// window between attempts. It fails the test when the frame never appears.
pub fn await_rendered(cx: &mut VisualTestContext, sel: &'static str, attempts: usize) {
    for _ in 0..attempts {
        if cx.debug_bounds(sel).is_some() {
            return;
        }
        pump(cx);
    }
    panic!("window never rendered {sel:?}");
}

/// rendered reports whether the current frame contains a selector. Not every
/// suite needs it, so an unused import is allowed here.
#[allow(dead_code)]
pub fn rendered(cx: &mut VisualTestContext, sel: &'static str) -> bool {
    cx.debug_bounds(sel).is_some()
}
