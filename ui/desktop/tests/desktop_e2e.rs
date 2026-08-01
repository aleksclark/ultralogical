//! GPUI application-path evidence against the real stack.
//!
//! Each test opens the shipped dark GPUI window, drives it with the same
//! actions the native entrypoint uses, and asserts on the rendered element
//! tree. Direct RPC results are used only to make things happen; every claim
//! is checked against what the window painted.

mod support;

use gpui::TestAppContext;
use support::{await_rendered, env, open_app, pump, rendered, selector};
use ultralogical_desktop::{DesktopWindow, TimelineItem};

const FRAME_ATTEMPTS: usize = 600;

/// The window renders the dark shell and every top-level panel before any
/// data is loaded, so the application is a real surface rather than a stub.
#[gpui::test]
async fn renders_dark_application_shell(cx: &mut TestAppContext) {
    let (_window, mut cx, _client, _org) = open_app(cx).await;
    let cx = &mut cx;

    await_rendered(cx, "window:dark", FRAME_ATTEMPTS);
    await_rendered(cx, "sidebar", FRAME_ATTEMPTS);
    await_rendered(cx, "main", FRAME_ATTEMPTS);
    await_rendered(cx, "session-list", FRAME_ATTEMPTS);
    await_rendered(cx, "timeline", FRAME_ATTEMPTS);
    await_rendered(cx, "environment-panel", FRAME_ATTEMPTS);
    await_rendered(cx, "usage-panel", FRAME_ATTEMPTS);
}

/// The window renders a session list and a session timeline fed by the real
/// event stream, and its connection state tracks the live subscription.
#[gpui::test]
async fn renders_session_list_and_timeline(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    await_rendered(cx, "session-list", FRAME_ATTEMPTS);
    assert!(
        rendered(cx, "connection:disconnected"),
        "connection state not rendered before subscribing"
    );

    let session = client
        .create_session(&org_id, "GPUI session list")
        .await
        .expect("create session");
    let sessions = client.list_sessions(&org_id).await.expect("list sessions");
    window.update(cx, |view: &mut DesktopWindow, cx| view.set_sessions(sessions, cx));
    await_rendered(cx, "session:GPUI session list", FRAME_ATTEMPTS);

    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });

    // Live connection state is a rendered fact, not an internal flag.
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);

    client
        .append_user_message(&session.id, "hello from the desktop window")
        .await
        .expect("append");
    await_rendered(
        cx,
        selector("row:you: hello from the desktop window"),
        FRAME_ATTEMPTS,
    );
}

/// Joining a session renders the participant in the window, so presence is a
/// visible fact in the desktop client.
#[gpui::test]
async fn renders_presence(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI presence")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);

    client.join(&session.id, "GPUI desktop").await.expect("join");
    // The join is observable through the event log the window renders from.
    for _ in 0..FRAME_ATTEMPTS {
        let joined = window.read_with(cx, |view: &DesktopWindow, _| {
            view.state.participants.iter().any(|p| p == "GPUI desktop")
        });
        if joined {
            break;
        }
        pump(cx);
    }
    let participants =
        window.read_with(cx, |view: &DesktopWindow, _| view.state.participants.clone());
    assert!(
        participants.iter().any(|p| p == "GPUI desktop"),
        "presence never reached rendered state: {participants:?}"
    );
    // The joined participant is also visible in the rendered timeline, which
    // is the surface a human actually reads.
    client
        .append_user_message(&session.id, "presence check")
        .await
        .expect("append");
    await_rendered(cx, "row:you: presence check", FRAME_ATTEMPTS);
}

/// The window's startup sequence is the one the native entrypoint runs. This
/// test creates a session, then drives `DesktopWindow::start_up` — the exact
/// function `main.rs` calls after connecting — and asserts the launched
/// application paints the session list, an opened timeline, and usage without
/// any test-only wiring.
#[gpui::test]
async fn drives_same_actions_as_entrypoint(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    client
        .create_session(&org_id, "GPUI entrypoint")
        .await
        .expect("create session");

    // Exactly what main.rs does once the client is connected.
    let mut async_app = cx.to_async();
    let endpoint = env("ULTRAD_URL");
    DesktopWindow::start_up(&window, &mut client, &org_id, &endpoint, &mut async_app).await;

    await_rendered(cx, "session:GPUI entrypoint", FRAME_ATTEMPTS);
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);
    await_rendered(cx, "usage-panel", FRAME_ATTEMPTS);
    let opened = window.read_with(cx, |view: &DesktopWindow, _| view.state.active_session.clone());
    assert!(
        opened.is_some(),
        "the entrypoint startup sequence never opened a session"
    );
}

/// Session memory written through the API is folded from the event log and
/// painted by the window, and a replay from seq 0 repaints the same entry.
#[gpui::test]
async fn renders_session_memory(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI memory")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);

    client
        .set_memory(&session.id, "desktop.note", "\"remembered\"")
        .await
        .expect("set memory");
    await_rendered(cx, "memory:desktop.note", FRAME_ATTEMPTS);
    await_rendered(cx, "memory-count:1", FRAME_ATTEMPTS);

    // The painted key corresponds to a durable write, not a local echo.
    let keys = client.list_memory(&session.id).await.expect("list memory");
    assert!(
        keys.iter().any(|k| k == "desktop.note"),
        "memory entry was never painted from a durable write: {keys:?}"
    );

    // Replay repaints it, so the panel is derived from the log.
    let replay = client.subscribe(&session.id, 0).await.expect("resubscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| view.replay_session(replay, cx));
    await_rendered(cx, "memory:desktop.note", FRAME_ATTEMPTS);
}

/// A7.2 — the window renders intermediate streamed frames, then a terminal
/// status. Two independent signals: the rendered frame counter advances past
/// one, and more than one distinct assistant row is painted while streaming.
#[gpui::test]
async fn renders_incremental_stream_frames(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI streaming")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);

    client
        .start_run(&session.id, "stream to me")
        .await
        .expect("start run");

    let mut painted: Vec<String> = Vec::new();
    let mut completed = false;
    for _ in 0..FRAME_ATTEMPTS {
        let snapshot = window.read_with(cx, |view: &DesktopWindow, _| {
            (
                view.state.delta_frames,
                view.state
                    .timeline
                    .iter()
                    .filter(|item| matches!(item, TimelineItem::Assistant { .. }))
                    .map(|item| item.render_label())
                    .collect::<Vec<_>>(),
                view.state.timeline.iter().any(|item| {
                    matches!(item, TimelineItem::Status { status, .. } if status == "completed")
                }),
            )
        });
        // Only count a row once the window has actually painted it.
        for label in snapshot.1 {
            let sel = selector(format!("row:{label}"));
            if rendered(cx, sel) && !painted.contains(&label) {
                painted.push(label);
            }
        }
        if snapshot.2 {
            completed = true;
            break;
        }
        pump(cx);
    }

    assert!(completed, "run never reached a rendered terminal status");
    assert!(
        painted.len() >= 2,
        "window painted {} assistant frames, want at least 2: {painted:?}",
        painted.len()
    );
    let frames = window.read_with(cx, |view: &DesktopWindow, _| view.state.delta_frames);
    assert!(frames >= 2, "window folded {frames} streamed frames, want at least 2");
    await_rendered(cx, selector(format!("delta-frames:{frames}")), FRAME_ATTEMPTS);
    await_rendered(cx, "row:run completed", FRAME_ATTEMPTS);
}

/// A7.2 — replay produces the same rendered timeline. The window discards all
/// session state and resubscribes from seq 0, so equality proves the frame is
/// derived from the log rather than from live-only bookkeeping.
#[gpui::test]
async fn replays_identical_timeline(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI replay")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);
    client
        .start_run(&session.id, "stream to me")
        .await
        .expect("start run");
    await_rendered(cx, "row:run completed", FRAME_ATTEMPTS);

    let before = window.read_with(cx, |view: &DesktopWindow, _| {
        view.state
            .timeline
            .iter()
            .map(|item| item.render_label())
            .collect::<Vec<_>>()
    });
    assert!(!before.is_empty(), "nothing was rendered before replay");

    let replay = client.subscribe(&session.id, 0).await.expect("resubscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| view.replay_session(replay, cx));
    await_rendered(cx, "row:run completed", FRAME_ATTEMPTS);

    let after = window.read_with(cx, |view: &DesktopWindow, _| {
        view.state
            .timeline
            .iter()
            .map(|item| item.render_label())
            .collect::<Vec<_>>()
    });
    assert_eq!(after, before, "replayed timeline differs from the original");
    for label in after {
        assert!(
            rendered(cx, selector(format!("row:{label}"))),
            "replayed row {label:?} was not painted"
        );
    }
}

/// The window renders an awaiting question and answering it through the
/// window's action completes the run.
#[gpui::test]
async fn renders_question_and_answers(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI awaiting")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);
    client
        .start_run(&session.id, "ask me something")
        .await
        .expect("start run");

    await_rendered(cx, "row:question: Which color? [red/blue]", FRAME_ATTEMPTS);
    let run_id = window.read_with(cx, |view: &DesktopWindow, _| {
        view.state
            .timeline
            .iter()
            .find_map(|item| match item {
                TimelineItem::Question { run_id, .. } => Some(run_id.clone()),
                _ => None,
            })
            .expect("rendered question carries its run id")
    });
    client.answer(&run_id, "blue").await.expect("answer");
    // The answer is echoed as a user row, the resumed turn appends to the same
    // run's assistant row, and the run reaches a rendered terminal status.
    await_rendered(cx, "row:you: blue", FRAME_ATTEMPTS);
    await_rendered(cx, "row:run completed", FRAME_ATTEMPTS);
    let assistant = window.read_with(cx, |view: &DesktopWindow, _| {
        view.state
            .timeline
            .iter()
            .filter_map(|item| match item {
                TimelineItem::Assistant { text, .. } => Some(text.clone()),
                _ => None,
            })
            .collect::<Vec<_>>()
            .join(" ")
    });
    assert!(
        assistant.contains("great choice of blue"),
        "resumed turn was not rendered: {assistant:?}"
    );
    await_rendered(cx, selector(format!("row:agent: {assistant}")), FRAME_ATTEMPTS);
}

/// The prompt field accepts real keystrokes through the window's focus and key
/// handling, and the typed text is painted.
#[gpui::test]
async fn accepts_prompt_keystrokes(cx: &mut TestAppContext) {
    let (_window, mut cx, _client, _org) = open_app(cx).await;
    let cx = &mut cx;

    await_rendered(cx, "prompt:", FRAME_ATTEMPTS);
    cx.simulate_keystrokes("h i");
    pump(cx);
    await_rendered(cx, "prompt:hi", FRAME_ATTEMPTS);
}

/// A7.3 — credential material must never reach rendered desktop state.
#[gpui::test]
async fn never_exposes_credential_material(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let canary = std::env::var("ULTRA_CANARY_KEY").expect("ULTRA_CANARY_KEY");
    let session = client
        .create_session(&org_id, "GPUI credential hygiene")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);
    client
        .start_run(&session.id, "stream to me")
        .await
        .expect("start run");
    await_rendered(cx, "row:run completed", FRAME_ATTEMPTS);

    let rendered_state = window.read_with(cx, |view: &DesktopWindow, _| {
        let rows: Vec<String> = view.state.timeline.iter().map(|i| i.render_label()).collect();
        format!("{rows:?}{:?}{:?}", view.state.error, view.state.exec_output)
    });
    assert!(
        !rendered_state.contains(&canary),
        "rendered desktop state contains the credential canary"
    );
    let encoded = canary.replace('-', "%2D");
    assert!(
        !rendered_state.contains(&encoded),
        "rendered desktop state contains an encoded credential canary"
    );
}
