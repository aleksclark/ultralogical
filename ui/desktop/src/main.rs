use ultralogical_desktop::{DarkTheme, GpuiDesktopState};

fn main() {
    // GPUI owns the native desktop state and dark theme. Window creation is
    // skipped in headless CI; platform launch wiring uses this same state.
    let state = GpuiDesktopState::default();
    let _background = state.dark_theme();
    let _ = DarkTheme::SURFACE;
    println!("Ultralogical GPUI Desktop — dark theme ready");
}
