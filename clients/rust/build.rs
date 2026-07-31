fn main() -> Result<(), Box<dyn std::error::Error>> {
    let protoc = protoc_bin_vendored::protoc_bin_path()?;
    unsafe { std::env::set_var("PROTOC", protoc); }
    let protos = [
        "../../proto/ultra/v1/org.proto",
        "../../proto/ultra/v1/session.proto",
        "../../proto/ultra/v1/event.proto",
        "../../proto/ultra/v1/agent.proto",
        "../../proto/ultra/v1/env.proto",
        "../../proto/ultra/v1/flow.proto",
        "../../proto/ultra/v1/automation.proto",
    ];
    tonic_prost_build::configure()
        .build_server(false)
        .build_client(true)
        .compile_protos(&protos, &["../../proto"])?;
    println!("cargo:rerun-if-changed=../../proto/ultra/v1");
    Ok(())
}
