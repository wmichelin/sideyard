variable "subscription_id" { type = string }
variable "operator_object_id" { type = string }
variable "operator_login" { type = string }
variable "environment" {
  type = string
  validation {
    condition     = contains(["staging", "production"], var.environment)
    error_message = "Environment must be staging or production."
  }
}
variable "location" {
  type    = string
  default = "centralus"
}
variable "sql_server_name" {
  type = string
  validation {
    condition     = can(regex("^sideyard-[a-z0-9-]{1,45}$", var.sql_server_name))
    error_message = "Use a globally unique sideyard- prefixed SQL server name."
  }
}
variable "storage_account_name" {
  type = string
  validation {
    condition     = can(regex("^sideyard[a-z0-9]{1,16}$", var.storage_account_name))
    error_message = "Use a globally unique sideyard-prefixed storage account name."
  }
}
variable "allowed_ipv4" {
  type = set(string)
  validation {
    condition     = length(var.allowed_ipv4) > 0 && alltrue([for ip in var.allowed_ipv4 : can(cidrhost("${ip}/32", 0)) && ip != "0.0.0.0"])
    error_message = "Supply explicit operator/app IPv4 addresses; never the allow-Azure-services wildcard."
  }
}
variable "runtime_object_ids" {
  description = "Entra service principal object IDs granted only private-photo data access."
  type        = set(string)
  default     = []
}
variable "backup_object_ids" {
  description = "Separate export identities; cannot access photos or delete backup versions."
  type        = set(string)
  default     = []
}
