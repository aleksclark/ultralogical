# Fleet Nomad job for ultracore (home datacenter).
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
      port "http" {
        # Private bind; no Traefik tags — operator network only.
      }
    }

    service {
      name     = "coreadmin"
      port     = "http"
      provider = "nomad"
      # Intentionally no traefik.* tags.

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
        DATABASE_URL              = "${DATABASE_URL}"
        CORE_ADMIN_ADDR           = ":${NOMAD_PORT_http}"
        CORE_ADMIN_TOKEN          = "${CORE_ADMIN_TOKEN}"
        CORE_ADMIN_TOKEN_ROLE     = "${CORE_ADMIN_TOKEN_ROLE}"
        CORE_ADMIN_TOKENS         = "${CORE_ADMIN_TOKENS}"
        CORE_ADMIN_CURSOR_SECRET  = "${CORE_ADMIN_CURSOR_SECRET}"
        CORE_ADMIN_REVEAL_ENABLED = "false"
        CORE_ADMIN_ENABLE_TERMINATE = "false"
        CORE_ADMIN_ENABLE_SUSPEND   = "false"
        CORE_MASTER_KEY           = "${CORE_MASTER_KEY}"
        CORE_MIGRATE              = "true"
      }

      resources {
        cpu    = 250
        memory = 256
      }
    }
  }

}
