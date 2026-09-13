terraform {
  required_version = "= 1.16.2"
  required_providers {
    exedev = {
      source  = "noebs/exedev"
      version = "0.1.0"
    }
  }
}

provider "exedev" {}

variable "machines" {
  type = map(object({
    image       = string
    cpus        = number
    memory_gib  = number
    disk_gib    = number
    role        = string
    public_http = bool
  }))
  nullable = false
  validation {
    condition = alltrue([
      for name, machine in var.machines :
      startswith(name, "noebs-") && can(regex("@sha256:[0-9a-f]{64}$", machine.image)) &&
      contains(["control", "data", "workers", "backup", "telegram"], machine.role) &&
      machine.public_http == (name == "noebs-workers" && machine.role == "workers")
    ])
    error_message = "Use noebs- VM names, immutable image digests and explicit roles; only noebs-workers serves public HTTP."
  }
}

resource "exedev_vm" "machine" {
  for_each   = var.machines
  name       = each.key
  image      = each.value.image
  cpus       = each.value.cpus
  memory_gib = each.value.memory_gib
  disk_gib   = each.value.disk_gib
  private    = !each.value.public_http

  lifecycle {
    prevent_destroy = true
  }
}

output "machines" {
  value = {
    for name, machine in exedev_vm.machine : name => {
      ssh_destination = machine.ssh_destination
      role            = var.machines[name].role
      region          = machine.region
    }
  }
}
