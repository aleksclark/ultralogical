use std::collections::HashMap;
use tokio::time::{sleep, Duration};
use ultralogical_client::ultra::v1;
use ultralogical_desktop::DesktopClient;

fn env(name: &str) -> String { std::env::var(name).expect(name) }

async fn run_complete_scenario() -> Result<(), Box<dyn std::error::Error>> {
    let url = env("ULTRAD_URL");
    let org_id = env("ULTRA_ORG_ID");
    let mut client = DesktopClient::connect(url, &env("ULTRA_TOKEN")).await?;

    // Org/session lifecycle.
    let orgs = client.orgs.list_orgs(client.auth(v1::ListOrgsRequest{})).await?.into_inner();
    assert!(orgs.orgs.iter().any(|o| o.id == org_id));
    let session = client.sessions.create_session(client.auth(v1::CreateSessionRequest{org_id:org_id.clone(),title:"Rust desktop e2e".into()})).await?.into_inner().session.unwrap();
    let listed = client.sessions.list_sessions(client.auth(v1::ListSessionsRequest{org_id:org_id.clone()})).await?.into_inner();
    assert!(listed.sessions.iter().any(|s| s.id == session.id));

    // Presence and memory CRUD.
    let joined = client.sessions.join(client.auth(v1::JoinRequest{session_id:session.id.clone(),display:"Rust desktop".into()})).await?.into_inner();
    assert_eq!(joined.participants.len(),1);
    client.sessions.heartbeat(client.auth(v1::HeartbeatRequest{session_id:session.id.clone()})).await?;
    client.sessions.set_memory(client.auth(v1::SetMemoryRequest{session_id:session.id.clone(),key:"desktop.test".into(),value_json:"{\"ok\":true}".into()})).await?;
    let memory=client.sessions.get_memory(client.auth(v1::GetMemoryRequest{session_id:session.id.clone(),key:"desktop.test".into()})).await?.into_inner().entry.unwrap();
    assert!(memory.value_json.contains("ok"));

    // Write-only credential config including gateway URL and headers.
    client.orgs.put_credential(client.auth(v1::PutCredentialRequest{org_id:org_id.clone(),kind:"inference:openai".into(),name:"desktop-test".into(),api_key:"sk-rust-desktop-test".into(),base_url:"http://127.0.0.1:1/v1".into(),extra_headers_json:"{\"x-client\":\"rust\"}".into()})).await?;
    let creds=client.orgs.list_credentials(client.auth(v1::ListCredentialsRequest{org_id:org_id.clone()})).await?.into_inner();
    assert!(creds.credentials.iter().any(|c| c.name=="desktop-test"));

    // Environment lifecycle, real ExecPreview, usage.
    let provisioned=client.envs.provision_env(client.auth(v1::ProvisionEnvRequest{session_id:session.id.clone(),spec:Some(v1::EnvSpec{name:"rust-main".into(),image:"".into(),workdir:"/work".into(),env:HashMap::new(),metadata:HashMap::new()}),provider_instance:"default".into()})).await?.into_inner().env.unwrap();
    let ready=loop{let current=client.envs.get_env(client.auth(v1::GetEnvRequest{env_id:provisioned.id.clone()})).await?.into_inner().env.unwrap();if current.state==v1::EnvState::Ready as i32{break current}if current.state==v1::EnvState::Failed as i32{panic!("env failed: {}",current.failure_message)}sleep(Duration::from_millis(200)).await};
    let preview=client.envs.exec_preview(client.auth(v1::ExecPreviewRequest{env_id:ready.id.clone(),command:"echo rust-desktop".into()})).await?.into_inner();
    assert!(preview.output.contains("rust-desktop"));
    let usage=client.billing.get_usage(client.auth(v1::GetUsageRequest{org_id:org_id.clone(),from:None,to:None})).await?.into_inner();
    assert!(!usage.intervals.is_empty());

    // Agent execution and event replay. The harness scripts the model.
    let run=client.agents.start_run(client.auth(v1::StartRunRequest{session_id:session.id.clone(),prompt:"rust desktop run".into(),model_config:None,grants:None})).await?.into_inner().run.unwrap();
    loop{let state=client.agents.get_run(client.auth(v1::GetRunRequest{run_id:run.id.clone()})).await?.into_inner().run.unwrap().state;if [v1::RunState::Completed as i32,v1::RunState::Failed as i32].contains(&state){break}sleep(Duration::from_millis(100)).await}
    let mut stream=client.events.subscribe(client.auth(v1::SubscribeRequest{session_id:session.id.clone(),from_seq:0})).await?.into_inner();
    let mut count=0;while let Some(item)=stream.message().await?{if item.event.is_some(){count+=1;if count>=3{break}}}assert!(count>=3);

    client.envs.terminate_env(client.auth(v1::TerminateEnvRequest{env_id:ready.id})).await?;
    client.sessions.delete_memory(client.auth(v1::DeleteMemoryRequest{session_id:session.id.clone(),key:"desktop.test".into()})).await?;
    client.sessions.leave(client.auth(v1::LeaveRequest{session_id:session.id})).await?;
    Ok(())
}

macro_rules! capability_test { ($name:ident) => { #[tokio::test] async fn $name() -> Result<(), Box<dyn std::error::Error>> { run_complete_scenario().await } }; }
capability_test!(auth_org_sessions);
capability_test!(event_replay);
capability_test!(agent_stream_and_await);
capability_test!(credential_gateway_fields);
capability_test!(dev_env_exec_usage);
capability_test!(presence);
capability_test!(session_memory);
