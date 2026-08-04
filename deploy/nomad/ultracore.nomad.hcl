# Fleet Nomad job for ultracore (home datacenter).
# Render secrets via envsubst from a local deploy env file; never commit secrets.
job "ultracore" {
  datacenters = ["home"]
  type        = "service"

  group "api" {
    count = 1

    network {
      port "http" {}
    }

    service {
      name     = "cored"
      port     = "http"
      provider = "nomad"

      tags = [
        "traefik.enable=true",
        "traefik.http.routers.cored.rule=Host(`core.fleet.clark.team`) || Host(`core.clark.team`)",
        "traefik.http.routers.cored.entrypoints=websecure",
        "traefik.http.routers.cored.tls.certresolver=letsencrypt",
      ]

      check {
        type     = "http"
        path     = "/readyz"
        interval = "10s"
        timeout  = "3s"
      }
    }

    task "cored" {
      driver = "docker"

      config {
        image      = "ghcr.io/aleksclark/ultracore:${IMAGE_TAG}"
        entrypoint = ["/usr/local/bin/cored"]
        ports      = ["http"]
        force_pull = false
      }

      env {
        DATABASE_URL       = "${DATABASE_URL}"
        CORE_MASTER_KEY    = "${CORE_MASTER_KEY}"
        CORE_ADDR          = ":${NOMAD_PORT_http}"
        CORE_MIGRATE       = "true"
        CORE_OTLP_ENDPOINT = "http://192.168.0.24:4317"
      }

      resources {
        cpu    = 500
        memory = 512
      }
    }
  }

  group "worker" {
    count = 1

    network {
      port "health" {}
    }

    service {
      name     = "coreworker"
      port     = "health"
      provider = "nomad"

      check {
        type     = "http"
        path     = "/readyz"
        interval = "10s"
        timeout  = "3s"
      }
    }

    task "coreworker" {
      driver = "docker"

      config {
        image      = "ghcr.io/aleksclark/ultracore:${IMAGE_TAG}"
        entrypoint = ["/usr/local/bin/coreworker"]
        ports      = ["health"]
        force_pull = false
      }

      env {
        DATABASE_URL     = "${DATABASE_URL}"
        CORE_MASTER_KEY  = "${CORE_MASTER_KEY}"
        CORE_ADDR        = ":${NOMAD_PORT_health}"
        CORE_MIGRATE     = "false"
        CORE_MAX_WORKERS = "10"
      }

      resources {
        cpu    = 500
        memory = 1024
      }
    }
  }

  group "admin" {
    count = 1

    network {
      port "http" {}
    }

    service {
      name     = "coreadmin"
      port     = "http"
      provider = "nomad"

      # Private operator SPA+API. Fleet DNS already points
      # core-admin.fleet.clark.team at the Traefik edge; TLS via letsencrypt.
      # Browser login still requires CORE_ADMIN_TOKEN — this is not anonymous.
      tags = [
        "traefik.enable=true",
        "traefik.http.routers.coreadmin.rule=Host(`core-admin.fleet.clark.team`)",
        "traefik.http.routers.coreadmin.entrypoints=websecure",
        "traefik.http.routers.coreadmin.tls.certresolver=letsencrypt",
      ]

      check {
        type     = "http"
        path     = "/readyz"
        interval = "10s"
        timeout  = "3s"
      }
    }

    task "coreadmin" {
      driver = "docker"

      config {
        image      = "ghcr.io/aleksclark/ultracore:${IMAGE_TAG}"
        entrypoint = ["/usr/local/bin/coreadmin"]
        ports      = ["http"]
        force_pull = false
      }

      env {
        DATABASE_URL                = "${DATABASE_URL}"
        CORE_ADMIN_ADDR             = ":${NOMAD_PORT_http}"
        CORE_ADMIN_TOKEN            = "${CORE_ADMIN_TOKEN}"
        CORE_ADMIN_TOKEN_ROLE       = "${CORE_ADMIN_TOKEN_ROLE}"
        CORE_ADMIN_CURSOR_SECRET    = "${CORE_ADMIN_CURSOR_SECRET}"
        CORE_ADMIN_REVEAL_ENABLED   = "false"
        CORE_ADMIN_ENABLE_TERMINATE = "false"
        CORE_ADMIN_ENABLE_SUSPEND   = "false"
        CORE_MASTER_KEY             = "${CORE_MASTER_KEY}"
        CORE_MIGRATE                = "false"
      }

      resources {
        cpu    = 250
        memory = 512
      }
    }
  }
}
