# Fleet Nomad job for ultracore (product of aleksclark/ultralogical).
# Authoritative project-owned jobspec. Secrets come from Nomad Variables
# at nomad/jobs/ultracore (key names only in git). Image is digest-pinned;
# never deploy :latest as authority.
#
# Variable keys (create once; values never in git):
#   database_url, master_key, admin_token, admin_token_role, admin_cursor_secret
#
# Image digest is maintained in deploy/nomad/images.lock.hcl and substituted
# below as a full ghcr.io/...@sha256:... reference.

job "ultracore" {
  datacenters = ["home"]
  type        = "service"

  meta {
    owner       = "ultralogical"
    managed_by  = "project"
    app         = "ultracore"
    source_repo = "aleksclark/ultralogical"
  }

  update {
    max_parallel      = 1
    health_check      = "checks"
    min_healthy_time  = "15s"
    healthy_deadline  = "5m"
    progress_deadline = "10m"
    stagger           = "15s"
  }

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
        # DIGEST_PIN: release workflow / deploy lock updates this immutable ref.
        image      = "ghcr.io/aleksclark/ultracore@sha256:2f595cd12a30f5b50ec1bacf7546d1ae8cf3ce76cebac46005c9db27b0e4f788"
        entrypoint = ["/usr/local/bin/cored"]
        ports      = ["http"]
        force_pull = true
      }

      template {
        destination = "secrets/cored.env"
        env         = true
        change_mode = "restart"
        data        = <<-EOT
          {{- with nomadVar "nomad/jobs/ultracore" -}}
          DATABASE_URL={{ .database_url }}
          CORE_MASTER_KEY={{ .master_key }}
          {{- end }}
          CORE_ADDR=:{{ env "NOMAD_PORT_http" }}
          CORE_MIGRATE=true
          CORE_OTLP_ENDPOINT=http://192.168.0.24:4317
        EOT
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
        image      = "ghcr.io/aleksclark/ultracore@sha256:2f595cd12a30f5b50ec1bacf7546d1ae8cf3ce76cebac46005c9db27b0e4f788"
        entrypoint = ["/usr/local/bin/coreworker"]
        ports      = ["health"]
        force_pull = true
      }

      template {
        destination = "secrets/coreworker.env"
        env         = true
        change_mode = "restart"
        data        = <<-EOT
          {{- with nomadVar "nomad/jobs/ultracore" -}}
          DATABASE_URL={{ .database_url }}
          CORE_MASTER_KEY={{ .master_key }}
          {{- end }}
          CORE_ADDR=:{{ env "NOMAD_PORT_health" }}
          CORE_MIGRATE=false
          CORE_MAX_WORKERS=10
        EOT
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

      # Private operator SPA+API on internal fleet hostname only.
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
        image      = "ghcr.io/aleksclark/ultracore@sha256:2f595cd12a30f5b50ec1bacf7546d1ae8cf3ce76cebac46005c9db27b0e4f788"
        entrypoint = ["/usr/local/bin/coreadmin"]
        ports      = ["http"]
        force_pull = true
      }

      template {
        destination = "secrets/coreadmin.env"
        env         = true
        change_mode = "restart"
        data        = <<-EOT
          {{- with nomadVar "nomad/jobs/ultracore" -}}
          DATABASE_URL={{ .database_url }}
          CORE_MASTER_KEY={{ .master_key }}
          CORE_ADMIN_TOKEN={{ .admin_token }}
          CORE_ADMIN_TOKEN_ROLE={{ .admin_token_role }}
          CORE_ADMIN_CURSOR_SECRET={{ .admin_cursor_secret }}
          {{- end }}
          CORE_ADMIN_ADDR=:{{ env "NOMAD_PORT_http" }}
          CORE_ADMIN_REVEAL_ENABLED=false
          CORE_ADMIN_ENABLE_TERMINATE=false
          CORE_ADMIN_ENABLE_SUSPEND=false
          CORE_MIGRATE=false
        EOT
      }

      resources {
        cpu    = 250
        memory = 512
      }
    }
  }
}
