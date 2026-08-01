//! GPUI application-path evidence for flows (A9.8).
//!
//! Every assertion is made against what the real window painted, via
//! `debug_bounds`. Direct client calls appear only to make things happen; the
//! claims are always about the rendered frame.

mod support;

use gpui::TestAppContext;
use support::{await_rendered, env, open_app, open_app_at, pump, rendered, selector};
use ultralogical_desktop::{DesktopClient, DesktopWindow, WindowEntity};

const FRAME_ATTEMPTS: usize = 1200;

const SINGLE_AGENT_FLOW: &str = r#"{
  "params": {"subject": {"type": "string", "required": true}},
  "agents": {"reviewer": {"prompt": "desktop flow reviewer: {{.subject}}", "entry": true, "tools": ["post_event"]}}
}"#;

const SINGLE_AGENT_FLOW_V2: &str = r#"{
  "params": {"subject": {"type": "string", "required": true}},
  "agents": {"reviewer": {"prompt": "desktop flow reviewer v2: {{.subject}}", "entry": true, "tools": ["post_event"]}}
}"#;

const SLOW_FLOW: &str = r#"{
  "agents": {"slow": {"prompt": "desktop flow slow: keep going", "entry": true, "tools": ["post_event"]}}
}"#;

const INVALID_FLOW: &str = r#"{"agents":{"reviewer":{"prompt":"{{","entry":true,"tools":["bsh"]}}}"#;

/// unique_name keeps each test's flows apart on a shared stack.
fn unique_name(prefix: &str) -> String {
    format!("{prefix}-{}", uuid::Uuid::new_v4().simple())
}

/// refresh_catalog loads the catalog into the window, which is what the native
/// entrypoint does whenever the flow list changes.
async fn refresh_catalog(
    window: &WindowEntity,
    cx: &mut gpui::VisualTestContext,
    client: &mut DesktopClient,
    org_id: &str,
) {
    let flows = client.list_flows(org_id).await.expect("list flows");
    window.update(cx, |view: &mut DesktopWindow, cx| view.set_flows(flows, cx));
}

/// refresh_invocations loads the session's invocations into the window.
async fn refresh_invocations(
    window: &WindowEntity,
    cx: &mut gpui::VisualTestContext,
    client: &mut DesktopClient,
    session_id: &str,
) {
    let invocations = client.list_invocations(session_id).await.expect("list invocations");
    window.update(cx, |view: &mut DesktopWindow, cx| view.set_invocations(invocations, cx));
}

/// A9.8 — the rendered catalog lists org flows, and selecting a version shows
/// that version's own definition rather than always the latest.
#[gpui::test]
async fn renders_flow_catalog_and_version_selection(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let name = unique_name("desktop-catalog");
    client
        .put_flow(&org_id, &name, SINGLE_AGENT_FLOW)
        .await
        .expect("put flow")
        .expect("flow stored");
    refresh_catalog(&window, cx, &mut client, &org_id).await;
    await_rendered(cx, selector(format!("flow:{name}:1")), FRAME_ATTEMPTS);

    client
        .put_flow(&org_id, &name, SINGLE_AGENT_FLOW_V2)
        .await
        .expect("put flow")
        .expect("second version stored");
    refresh_catalog(&window, cx, &mut client, &org_id).await;
    await_rendered(cx, selector(format!("flow:{name}:2")), FRAME_ATTEMPTS);

    let versions = client
        .list_flow_versions(&org_id, &name)
        .await
        .expect("list versions");
    assert_eq!(versions.len(), 2, "expected two versions, got {}", versions.len());
    let name_for_window = name.clone();
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.set_flow_versions(name_for_window, versions, cx)
    });
    await_rendered(cx, "flow-version:2", FRAME_ATTEMPTS);
    await_rendered(cx, "flow-version:1", FRAME_ATTEMPTS);

    // The painted definition marker must change when an older version is
    // selected: a selector that always showed the latest would paint the same
    // marker for both.
    let latest_marker = window.read_with(cx, |view: &DesktopWindow, _| view.state.flow_definition.clone());
    window.update(cx, |view: &mut DesktopWindow, cx| view.select_flow_version(1, cx));
    let pinned_marker = window.read_with(cx, |view: &DesktopWindow, _| view.state.flow_definition.clone());
    assert_ne!(
        latest_marker, pinned_marker,
        "selecting version 1 did not change the shown definition"
    );
    assert!(
        pinned_marker.contains("desktop flow reviewer: "),
        "version 1 shows the wrong definition: {pinned_marker}"
    );
    assert!(
        !pinned_marker.contains("reviewer v2"),
        "version 1 shows version 2's definition"
    );
}

/// A9.1/A9.8 — the window renders the server's own typed validation errors,
/// and correcting the definition clears them and stores a version.
#[gpui::test]
async fn renders_flow_validation_errors(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let errors = client
        .validate_flow(&org_id, INVALID_FLOW)
        .await
        .expect("validate flow");
    assert!(!errors.is_empty(), "an invalid definition reported no errors");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.set_flow_definition(INVALID_FLOW.to_string(), cx);
        view.set_flow_errors(errors, cx);
    });
    await_rendered(cx, "flow-error:agents.reviewer.prompt:invalid_template", FRAME_ATTEMPTS);
    await_rendered(cx, "flow-error:agents.reviewer.tools[0]:unknown_tool", FRAME_ATTEMPTS);
    await_rendered(cx, "flow-errors:2", FRAME_ATTEMPTS);

    // Storing the same definition is refused with the same structured list, so
    // a user cannot save what the window just rejected.
    let name = unique_name("desktop-invalid");
    let refused = client
        .put_flow(&org_id, &name, INVALID_FLOW)
        .await
        .expect("put flow call");
    let refused_errors = refused.expect_err("an invalid definition was stored");
    assert!(
        refused_errors
            .iter()
            .any(|error| error.path == "agents.reviewer.prompt" && error.code == "invalid_template"),
        "a refused write lost its field paths: {refused_errors:?}"
    );
    refresh_catalog(&window, cx, &mut client, &org_id).await;
    assert!(
        !rendered(cx, selector(format!("flow:{name}:1"))),
        "a rejected definition appeared in the catalog"
    );

    // Correcting it clears the rendered errors and stores a version.
    client
        .put_flow(&org_id, &name, SINGLE_AGENT_FLOW)
        .await
        .expect("put flow")
        .expect("corrected flow stored");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.set_flow_definition(SINGLE_AGENT_FLOW.to_string(), cx);
        view.set_flow_errors(Vec::new(), cx);
    });
    refresh_catalog(&window, cx, &mut client, &org_id).await;
    await_rendered(cx, selector(format!("flow:{name}:1")), FRAME_ATTEMPTS);
    // The frame now reports zero validation errors, which is what a user sees
    // after correcting a definition.
    await_rendered(cx, "flow-errors:0", FRAME_ATTEMPTS);
}

/// A9.2/A9.4/A9.8 — invoking from the window paints provenance, ordered
/// progress, and the topology, and reaches a terminal state.
#[gpui::test]
async fn renders_flow_invocation_progress(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let name = unique_name("desktop-invoke");
    client
        .put_flow(&org_id, &name, SINGLE_AGENT_FLOW)
        .await
        .expect("put flow")
        .expect("flow stored");
    let session = client
        .create_session(&org_id, "GPUI flow invoke")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);
    refresh_catalog(&window, cx, &mut client, &org_id).await;

    let invocation_id = client
        .invoke_flow(&session.id, &name, 0, r#"{"subject":"desktop subject"}"#)
        .await
        .expect("invoke flow");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.select_invocation(Some(invocation_id.clone()), cx)
    });

    // Progress must accumulate in the painted frame rather than appear whole:
    // a window that only painted the final state would never show more than
    // the terminal entry.
    let mut painted_states = Vec::new();
    let mut completed = false;
    for _ in 0..FRAME_ATTEMPTS {
        refresh_invocations(&window, cx, &mut client, &session.id).await;
        let state = window.read_with(cx, |view: &DesktopWindow, _| {
            view.state.active_invocation_view().map(|inv| inv.state.clone())
        });
        if let Some(state) = state {
            if painted_states.last() != Some(&state) {
                painted_states.push(state.clone());
            }
            if state == "completed" || state == "failed" || state == "cancelled" {
                completed = state == "completed";
                break;
            }
        }
        pump(cx);
    }
    assert!(completed, "the invocation never completed; states seen: {painted_states:?}");

    await_rendered(cx, "invocation-state:completed", FRAME_ATTEMPTS);
    await_rendered(cx, "invocation-reason:completed", FRAME_ATTEMPTS);
    await_rendered(
        cx,
        selector(format!("provenance:{name}:1:{}", invocation_id.chars().take(8).collect::<String>())),
        FRAME_ATTEMPTS,
    );
    await_rendered(cx, "progress:accepted", FRAME_ATTEMPTS);
    await_rendered(cx, "progress:stage_started:0", FRAME_ATTEMPTS);
    await_rendered(cx, "progress:terminal", FRAME_ATTEMPTS);
    // The topology links the declared agent to the run it produced.
    await_rendered(cx, "flow-run:reviewer:completed", FRAME_ATTEMPTS);

    let keys = window.read_with(cx, |view: &DesktopWindow, _| {
        view.state.active_invocation_view().map(|inv| inv.progress_keys()).unwrap_or_default()
    });
    assert_eq!(
        keys.last().map(String::as_str),
        Some("terminal"),
        "progress does not end at the terminal step: {keys:?}"
    );
    let runs = window.read_with(cx, |view: &DesktopWindow, _| {
        view.state.active_invocation_view().map(|inv| inv.runs.clone()).unwrap_or_default()
    });
    assert_eq!(runs.len(), 1, "expected one run, got {}", runs.len());
    assert!(!runs[0].id.is_empty(), "the topology row links no run");
}

/// A9.6/A9.8 — cancelling from the rendered control converges the invocation,
/// and reconnecting through another replica rebuilds the same state.
#[gpui::test]
async fn cancels_and_recovers_flow_invocation(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let name = unique_name("desktop-cancel");
    client
        .put_flow(&org_id, &name, SLOW_FLOW)
        .await
        .expect("put flow")
        .expect("flow stored");
    let session = client
        .create_session(&org_id, "GPUI flow cancel")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);

    let invocation_id = client
        .invoke_flow(&session.id, &name, 0, "{}")
        .await
        .expect("invoke flow");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.select_invocation(Some(invocation_id.clone()), cx)
    });

    // Wait until the agent is actually running, so this cancels execution
    // rather than a queued launch.
    let mut running = false;
    for _ in 0..FRAME_ATTEMPTS {
        refresh_invocations(&window, cx, &mut client, &session.id).await;
        if rendered(cx, "invocation-state:running") {
            running = true;
            break;
        }
        pump(cx);
    }
    assert!(running, "the invocation never reached running");

    // Cancel through the window's own action, which the rendered control also
    // invokes; there is no test-only cancellation path.
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.request_cancel_invocation(invocation_id.clone(), cx)
    });

    let mut cancelled = false;
    for _ in 0..FRAME_ATTEMPTS {
        refresh_invocations(&window, cx, &mut client, &session.id).await;
        if rendered(cx, "invocation-state:cancelled") {
            cancelled = true;
            break;
        }
        pump(cx);
    }
    assert!(cancelled, "the invocation never converged on cancelled");
    await_rendered(cx, "invocation-reason:cancelled", FRAME_ATTEMPTS);
    let before = window.read_with(cx, |view: &DesktopWindow, _| {
        view.state.active_invocation_view().map(|inv| inv.progress_keys()).unwrap_or_default()
    });

    // Reconnect through the other replica. Nothing carries over in memory, so
    // identical rendered state proves it was rebuilt from the server.
    let alt = env("ULTRAD_ALT_URL");
    let (other, mut other_cx, mut other_client, _) = open_app_at(cx, &alt).await;
    let other_cx = &mut other_cx;
    let stream = other_client.subscribe(&session.id, 0).await.expect("resubscribe");
    other.update(other_cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(other_cx, "connection:live", FRAME_ATTEMPTS);
    refresh_invocations(&other, other_cx, &mut other_client, &session.id).await;
    other.update(other_cx, |view: &mut DesktopWindow, cx| {
        view.select_invocation(Some(invocation_id.clone()), cx)
    });
    await_rendered(other_cx, "invocation-state:cancelled", FRAME_ATTEMPTS);
    await_rendered(other_cx, "invocation-reason:cancelled", FRAME_ATTEMPTS);
    let after = other.read_with(other_cx, |view: &DesktopWindow, _| {
        view.state.active_invocation_view().map(|inv| inv.progress_keys()).unwrap_or_default()
    });
    assert_eq!(before, after, "the reconnected window rebuilt different progress");
}
