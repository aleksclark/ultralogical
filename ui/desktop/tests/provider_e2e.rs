use ultralogical_client::ultra::v1;
use ultralogical_desktop::DesktopClient;

fn env(name: &str) -> String { std::env::var(name).expect(name) }

#[tokio::test]
async fn registers_provider_kinds() -> Result<(), Box<dyn std::error::Error>> {
    let ui=ultralogical_desktop::GpuiDesktopState{core:Default::default(),active_panel:"providers".into(),provider_kinds:vec!["byo_k8s".into(),"byo_nomad".into(),"tunnel_local".into()]};
    assert_eq!(ui.dark_theme(),ultralogical_desktop::DarkTheme::BACKGROUND);
    assert!(ui.provider_kinds.contains(&"byo_k8s".to_string()));
    let mut client=DesktopClient::connect(env("ULTRAD_URL"),&env("ULTRA_TOKEN")).await?;
    let org=env("ULTRA_ORG_ID");
    for (kind,name,config) in [("byo_k8s","desktop-k8s","{\"mode\":\"loopback\"}"),("byo_nomad","desktop-nomad","{\"mode\":\"loopback\"}"),("tunnel_local","desktop-tunnel","{\"mode\":\"loopback\"}")] {
        client.orgs.register_provider(client.auth(v1::RegisterProviderRequest{org_id:org.clone(),kind:kind.into(),name:name.into(),config_json:config.into()})).await?;
    }
    let providers=client.orgs.list_providers(client.auth(v1::ListProvidersRequest{org_id:org})).await?.into_inner();
    assert!(providers.providers.iter().any(|p|p.kind=="byo_k8s"));
    assert!(providers.providers.iter().any(|p|p.kind=="byo_nomad"));
    assert!(providers.providers.iter().any(|p|p.kind=="tunnel_local"));
    Ok(())
}

#[tokio::test]
async fn rejects_invalid_provider_config() -> Result<(), Box<dyn std::error::Error>> {
    let mut client=DesktopClient::connect(env("ULTRAD_URL"),&env("ULTRA_TOKEN")).await?;
    let result=client.orgs.register_provider(client.auth(v1::RegisterProviderRequest{org_id:env("ULTRA_ORG_ID"),kind:"byo_k8s".into(),name:"bad".into(),config_json:"not-json".into()})).await;
    assert!(result.is_err());
    Ok(())
}
