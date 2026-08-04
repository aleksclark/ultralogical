# Nomad job example for aleks' fleet. Adjust datacenter/count/resources.
job "ultracore" {
  datacenters = ["dc1"]
  type        = "service"

  group "api" {
    count = 2

    network {
      port "http" { to = 8080 }
    }

    service {
      name = "cored"
      port = "http"
      check {
        type     = "http"
        path     = "/readyz"
        interval = "5s"
        timeout  = "2s"
      }
    }

    task "cored" {
      driver = "docker"
      config {
        image   = "ghcr.io/aleksclark/ultracore:0.1.0"
        command = "/usr/local/bin/cored"
        ports   = ["http"]
      }
      env {
        DATABASE_URL     = "postgres://core:core@postgres.service.consul:5432/core?sslmode=disable"
        CORE_MASTER_KEY  = "<set-via-vault-or-nomad-var>"
        CORE_ADDR        = ":8080"
        CORE_MIGRATE     = "false"
        CORE_OTLP_ENDPOINT = "http://otel-collector:4317"
      }
      resources {
        cpu    = 500
        memory = 512
      }
    }
  }

  group "worker" {
    count = 2

    network {
      port "health" { to = 8081 }
    }

    service {
      name = "coreworker"
      port = "health"
      check {
        type     = "http"
        path     = "/readyz"
        interval = "5s"
        timeout  = "2s"
      }
    }

    task "coreworker" {
      driver = "docker"
      config {
        image   = "ghcr.io/aleksclark/ultracore:0.1.0"
        command = "/usr/local/bin/coreworker"
        ports   = ["health"]
      }
      env {
        DATABASE_URL    = "postgres://core:core@postgres.service.consul:5432/core?sslmode=disable"
        CORE_MASTER_KEY = "<set-via-vault-or-nomad-var>"
        CORE_ADDR       = ":8081"
        CORE_MAX_WORKERS = "10"
      }
      resources {
        cpu    = 1000
        memory = 1024
      }
    }
  }
}
