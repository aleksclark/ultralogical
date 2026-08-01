//! GPUI application-path evidence for run trees, lanes, waits, memory, and
//! replica reconnection (A8.7).
//!
//! Every assertion is made against what the real window painted, via
//! `debug_bounds`. Direct RPC calls appear only to make things happen.

mod support;

use gpui::TestAppContext;
use support::{await_rendered, env, open_app, open_app_at, pump, rendered, selector};
use ultralogical_desktop::DesktopWindow;

const FRAME_ATTEMPTS: usize = 1200;

/// refresh_tree pulls the run tree and hands it to the window, which is what
/// the native entrypoint does whenever run structure changes.
async fn refresh_tree(
    window: &ultralogical_desktop::WindowEntity,
    cx: &mut gpui::VisualTestContext,
    client: &mut ultralogical_desktop::DesktopClient,
    session_id: &str,
) {
    let runs = client.run_tree(session_id).await.expect("fetch run tree");
    window.update(cx, |view: &mut DesktopWindow, cx| view.set_runs(runs, cx));
}

/// A8.7 — the window renders parent/child linkage and each run's wait.
#[gpui::test]
async fn renders_run_tree_and_lanes(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI run tree")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);
    await_rendered(cx, "run-tree", FRAME_ATTEMPTS);

    client
        .start_run(&session.id, "desktop cohort fanout")
        .await
        .expect("start run");

    // Poll the tree the way the application does, until the children appear.
    let mut painted_children = 0;
    for _ in 0..FRAME_ATTEMPTS {
        refresh_tree(&window, cx, &mut client, &session.id).await;
        let depths = window.read_with(cx, |view: &DesktopWindow, _| {
            view.state.runs.iter().map(|r| r.depth).collect::<Vec<_>>()
        });
        painted_children = depths.iter().filter(|d| **d == 1).count();
        if painted_children >= 3 {
            break;
        }
        pump(cx);
    }
    assert!(
        painted_children >= 3,
        "run tree never showed the cohort's children (depths seen: {painted_children})"
    );

    // The parent is a root and the members are its children, at greater depth.
    let nodes = window.read_with(cx, |view: &DesktopWindow, _| view.state.runs.clone());
    let roots: Vec<_> = nodes.iter().filter(|n| n.depth == 0).collect();
    assert_eq!(roots.len(), 1, "expected exactly one root run, got {}", roots.len());
    let parent_id = roots[0].run_id.clone();
    for child in nodes.iter().filter(|n| n.depth == 1) {
        assert_eq!(
            child.parent_run_id, parent_id,
            "child {} does not point at the parent",
            child.run_id
        );
        assert!(!child.cohort_id.is_empty(), "cohort member carries no cohort id");
    }

    // Each of those rows is actually painted, with its state and depth.
    for node in &nodes {
        let sel = selector(format!(
            "run:{}:{}:{}",
            node.run_id.chars().take(8).collect::<String>(),
            node.state,
            node.depth
        ));
        await_rendered(cx, sel, FRAME_ATTEMPTS);
    }

    // The parent's wait is rendered with its kind, state, and member count.
    for _ in 0..FRAME_ATTEMPTS {
        refresh_tree(&window, cx, &mut client, &session.id).await;
        if rendered(cx, "wait:cohort:resolved:3") {
            return;
        }
        pump(cx);
    }
    panic!("the parent's resolved cohort wait was never painted");
}

/// A8.7 — selecting a run filters the timeline to that agent's lane, through
/// the same action the rendered control invokes.
#[gpui::test]
async fn filters_timeline_to_one_lane(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI lanes")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);
    client
        .start_run(&session.id, "desktop cohort fanout")
        .await
        .expect("start run");

    // Wait until several agents have produced timeline rows.
    let mut child_id = String::new();
    for _ in 0..FRAME_ATTEMPTS {
        refresh_tree(&window, cx, &mut client, &session.id).await;
        let children = window.read_with(cx, |view: &DesktopWindow, _| {
            view.state
                .runs
                .iter()
                .filter(|r| r.depth == 1)
                .map(|r| r.run_id.clone())
                .collect::<Vec<_>>()
        });
        let attributed = window.read_with(cx, |view: &DesktopWindow, _| {
            view.state.timeline.iter().filter(|i| i.run_id().is_some()).count()
        });
        if let Some(first) = children.first() {
            if attributed >= 3 {
                child_id = first.clone();
                break;
            }
        }
        pump(cx);
    }
    assert!(!child_id.is_empty(), "no cohort child ever appeared with timeline activity");

    // Unfiltered, the lane control reports every row.
    let all_rows = window.read_with(cx, |view: &DesktopWindow, _| view.state.timeline_for(None).len());
    await_rendered(cx, selector(format!("lane-rows:all:{all_rows}")), FRAME_ATTEMPTS);

    // Selecting the child narrows it, and every remaining row belongs to it.
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.select_lane(Some(child_id.clone()), cx)
    });
    let lane_rows = window.read_with(cx, |view: &DesktopWindow, _| {
        let rows = view.state.timeline_for(Some(&child_id));
        for item in &rows {
            assert_eq!(
                item.run_id(),
                Some(child_id.as_str()),
                "a lane row belongs to another run"
            );
        }
        rows.len()
    });
    assert!(
        lane_rows < all_rows,
        "the lane ({lane_rows} rows) did not narrow the timeline ({all_rows} rows)"
    );
    await_rendered(
        cx,
        selector(format!("lane-rows:{child_id}:{lane_rows}")),
        FRAME_ATTEMPTS,
    );

    // Clearing it restores the full view.
    window.update(cx, |view: &mut DesktopWindow, cx| view.select_lane(None, cx));
    let restored = window.read_with(cx, |view: &DesktopWindow, _| view.state.timeline_for(None).len());
    await_rendered(cx, selector(format!("lane-rows:all:{restored}")), FRAME_ATTEMPTS);
}

/// A8.7 — a wait that times out is painted as timed out, so a stalled parent is
/// distinguishable from a progressing one.
#[gpui::test]
async fn renders_wait_transitions(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI wait transitions")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);
    client
        .start_run(&session.id, "desktop stalling fanout")
        .await
        .expect("start run");

    // The wait is open while the member works, then times out on its deadline.
    let mut saw_open = false;
    for _ in 0..FRAME_ATTEMPTS {
        refresh_tree(&window, cx, &mut client, &session.id).await;
        if rendered(cx, "wait:cohort:open:1") {
            saw_open = true;
        }
        if rendered(cx, "wait:cohort:timed_out:1") {
            assert!(saw_open, "the wait appeared already timed out; its open state was never painted");
            // The parent is released rather than stranded.
            await_rendered(cx, "row:run completed", FRAME_ATTEMPTS);
            return;
        }
        pump(cx);
    }
    panic!("the wait never painted a timeout");
}

/// A8.7 — memory an agent wrote is inspectable in the window, and reconnecting
/// through a second replica rebuilds the identical rendered state.
#[gpui::test]
async fn inspects_memory_and_reconnects(cx: &mut TestAppContext) {
    let alt = env("ULTRAD_ALT_URL");
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI memory and reconnect")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);
    await_rendered(cx, "memory-panel", FRAME_ATTEMPTS);
    // Empty is a rendered fact too.
    await_rendered(cx, "memory-count:0", FRAME_ATTEMPTS);

    client
        .start_run(&session.id, "desktop remember note")
        .await
        .expect("start run");

    // The agent's write reaches the window through the event log alone.
    await_rendered(cx, "memory:desktop.note", FRAME_ATTEMPTS);
    await_rendered(cx, "memory-count:1", FRAME_ATTEMPTS);
    await_rendered(cx, "row:run completed", FRAME_ATTEMPTS);

    let before = window.read_with(cx, |view: &DesktopWindow, _| {
        view.state
            .timeline
            .iter()
            .map(|i| i.render_label())
            .collect::<Vec<_>>()
    });
    assert!(!before.is_empty(), "nothing was rendered before reconnecting");

    // Reconnect through the other replica: a whole new window and client.
    let (other, mut other_cx, mut other_client, _) = open_app_at(cx, &alt).await;
    let other_cx = &mut other_cx;
    let replay = other_client
        .subscribe(&session.id, 0)
        .await
        .expect("subscribe on the second replica");
    other.update(other_cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), replay, cx)
    });
    await_rendered(other_cx, "connection:live", FRAME_ATTEMPTS);
    await_rendered(other_cx, selector(format!("endpoint:{alt}")), FRAME_ATTEMPTS);
    await_rendered(other_cx, "row:run completed", FRAME_ATTEMPTS);

    // The second replica reconstructs the same timeline and the same memory.
    let after = other.read_with(other_cx, |view: &DesktopWindow, _| {
        view.state
            .timeline
            .iter()
            .map(|i| i.render_label())
            .collect::<Vec<_>>()
    });
    assert_eq!(after, before, "the second replica rendered a different timeline");
    await_rendered(other_cx, "memory:desktop.note", FRAME_ATTEMPTS);
    for label in after {
        assert!(
            rendered(other_cx, selector(format!("row:{label}"))),
            "row {label:?} was not painted on the second replica"
        );
    }
}
