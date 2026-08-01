//! Native entrypoint: opens the real dark GPUI window, connects to ultrad,
//! lists sessions, and subscribes the first session's event stream.
//!
//! Configuration:
//!
//!   ULTRAD_URL     gRPC endpoint (default http://127.0.0.1:8080)
//!   ULTRA_TOKEN    bearer token (default dev-token)
//!   ULTRA_ORG_ID   org to open (default the first org the user belongs to)

use gpui::{App, AppContext, Application, Bounds, WindowBounds, WindowOptions, px, size};
use ultralogical_desktop::{
    DesktopClient, DesktopWindow, SESSION_WINDOW_SIZE, build_window, runtime, ultra_org_from_env,
};

fn main() {
    let url = std::env::var("ULTRAD_URL").unwrap_or_else(|_| "http://127.0.0.1:8080".into());
    let token = std::env::var("ULTRA_TOKEN").unwrap_or_else(|_| "dev-token".into());

    Application::new().run(move |cx: &mut App| {
        let (width, height) = SESSION_WINDOW_SIZE;
        let bounds = Bounds::centered(None, size(px(width), px(height)), cx);
        let handle = cx
            .open_window(
                WindowOptions {
                    window_bounds: Some(WindowBounds::Windowed(bounds)),
                    ..Default::default()
                },
                |_, cx| cx.new(build_window),
            )
            .expect("open desktop window");
        cx.activate(true);

        let url = url.clone();
        let token = token.clone();
        cx.spawn(async move |cx| {
            let connected = DesktopClient::connect(runtime::handle(), url, &token).await;
            let mut client = match connected {
                Ok(client) => client,
                Err(err) => {
                    let _ = handle.update(cx, |window: &mut DesktopWindow, _, cx| {
                        window.set_error(format!("connect failed: {err}"), cx);
                    });
                    return;
                }
            };
            let org_id = match ultra_org_from_env(&mut client).await {
                Ok(org_id) => org_id,
                Err(err) => {
                    let _ = handle.update(cx, |window: &mut DesktopWindow, _, cx| {
                        window.set_error(format!("no org available: {err}"), cx);
                    });
                    return;
                }
            };
            // The startup sequence lives in the window so UI tests drive the
            // same code path a launched application does.
            let entity = match handle.entity(cx) {
                Ok(entity) => entity,
                Err(_) => return,
            };
            DesktopWindow::start_up(&entity, &mut client, &org_id, cx).await;
        })
        .detach();
    });
}
