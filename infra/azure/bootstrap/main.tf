terraform {
  required_version = ">= 1.9.8, < 2.0.0"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "4.81.0"
    }
  }
}

provider "azurerm" {
  features {}
  subscription_id                 = var.subscription_id
  storage_use_azuread             = true
  resource_provider_registrations = "none"
}

variable "subscription_id" { type = string }
variable "operator_object_id" { type = string }
variable "storage_account_name" {
  type = string
  validation {
    condition     = can(regex("^[a-z0-9]{3,24}$", var.storage_account_name))
    error_message = "Use a globally unique lowercase Azure storage account name."
  }
}
variable "location" {
  type    = string
  default = "eastus"
}
variable "allowed_ipv4" {
  type = set(string)
  validation {
    condition     = length(var.allowed_ipv4) > 0 && alltrue([for ip in var.allowed_ipv4 : can(cidrhost("${ip}/32", 0))])
    error_message = "Provide explicit public IPv4 addresses for operators; no wildcard networks."
  }
}

resource "azurerm_resource_group" "state" {
  name     = "sideyard-terraform-state"
  location = var.location
  tags     = { application = "sideyard", purpose = "terraform-state" }
}

resource "azurerm_storage_account" "state" {
  name                            = var.storage_account_name
  resource_group_name             = azurerm_resource_group.state.name
  location                        = azurerm_resource_group.state.location
  account_tier                    = "Standard"
  account_replication_type        = "LRS"
  account_kind                    = "StorageV2"
  access_tier                     = "Hot"
  min_tls_version                 = "TLS1_2"
  https_traffic_only_enabled      = true
  shared_access_key_enabled       = false
  allow_nested_items_to_be_public = false
  default_to_oauth_authentication = true
  blob_properties {
    versioning_enabled = true
    delete_retention_policy { days = 7 }
    container_delete_retention_policy { days = 7 }
  }
  network_rules {
    default_action = "Deny"
    bypass         = ["None"]
    ip_rules       = var.allowed_ipv4
  }
  tags = azurerm_resource_group.state.tags
  lifecycle { prevent_destroy = true }
}

resource "azurerm_role_assignment" "operator" {
  scope                = azurerm_storage_account.state.id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = var.operator_object_id
}

resource "azurerm_storage_container" "state" {
  name                  = "tfstate"
  storage_account_id    = azurerm_storage_account.state.id
  container_access_type = "private"
  depends_on            = [azurerm_role_assignment.operator]
  lifecycle { prevent_destroy = true }
}

resource "azurerm_management_lock" "state" {
  name       = "preserve-terraform-state"
  scope      = azurerm_storage_account.state.id
  lock_level = "CanNotDelete"
  notes      = "Sideyard recovery state; remove only through a reviewed recovery operation."
}

output "backend" {
  value = {
    resource_group_name  = azurerm_resource_group.state.name
    storage_account_name = azurerm_storage_account.state.name
    container_name       = azurerm_storage_container.state.name
    use_azuread_auth     = true
    subscription_id      = var.subscription_id
  }
}
