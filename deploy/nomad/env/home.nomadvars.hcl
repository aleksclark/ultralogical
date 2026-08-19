# Non-secret fleet overlay for ultracore (plan 03 env).
# Secrets live only in Nomad Variables at nomad/jobs/ultracore.

datacenters = ["home"]

cored_hostname       = "core.fleet.clark.team"
cored_public_host    = "core.clark.team"
coreadmin_hostname   = "core-admin.fleet.clark.team"
otlp_endpoint        = "http://192.168.0.24:4317"

cored_cpu_mhz        = 500
cored_memory_mb      = 512
coreworker_cpu_mhz   = 500
coreworker_memory_mb = 1024
coreadmin_cpu_mhz    = 250
coreadmin_memory_mb  = 512
