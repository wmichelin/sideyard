terraform {
  required_version = ">= 1.9.8, < 2.0.0"
  backend "azurerm" {}
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "4.81.0"
    }
    azapi = {
      source  = "Azure/azapi"
      version = "2.12.0"
    }
  }
}

provider "azurerm" {
  features {}
  subscription_id                 = var.subscription_id
  storage_use_azuread             = true
  resource_provider_registrations = "none"
}

provider "azapi" {
  subscription_id            = var.subscription_id
  skip_provider_registration = true
}

data "azurerm_client_config" "current" {}

locals {
  tags                                = { application = "sideyard", environment = var.environment, managed_by = "terraform" }
  runtime_object_ids                  = toset([for id in var.runtime_object_ids : lower(id)])
  backup_object_ids                   = toset([for id in var.backup_object_ids : lower(id)])
  logical_backup_publisher_object_ids = toset([for id in var.logical_backup_publisher_object_ids : lower(id)])
  logical_photo_reader_object_ids     = toset([for id in var.logical_photo_reader_object_ids : lower(id)])
  terraform_runner_object_id          = lower(data.azurerm_client_config.current.object_id)
}

resource "azurerm_resource_group" "app" {
  name     = "sideyard-${var.environment}"
  location = var.location
  tags     = local.tags
}

resource "azurerm_mssql_server" "app" {
  name                          = var.sql_server_name
  resource_group_name           = azurerm_resource_group.app.name
  location                      = var.location
  version                       = "12.0"
  minimum_tls_version           = "1.2"
  public_network_access_enabled = true
  azuread_administrator {
    login_username              = var.operator_login
    object_id                   = var.operator_object_id
    tenant_id                   = data.azurerm_client_config.current.tenant_id
    azuread_authentication_only = true
  }
  tags = local.tags
  lifecycle { prevent_destroy = true }
}

resource "azurerm_mssql_firewall_rule" "approved_ip" {
  for_each         = var.allowed_ipv4
  name             = "approved-${replace(each.value, ".", "-")}"
  server_id        = azurerm_mssql_server.app.id
  start_ip_address = each.value
  end_ip_address   = each.value
}

# The free flag MUST be sent in the initial create request. No paid intermediate DB.
resource "azapi_resource" "database" {
  type      = "Microsoft.Sql/servers/databases@2025-01-01"
  name      = "sideyard"
  parent_id = azurerm_mssql_server.app.id
  location  = var.location
  body = {
    sku = { name = "GP_S_Gen5", tier = "GeneralPurpose", family = "Gen5", capacity = 1 }
    properties = {
      createMode                  = "Default"
      useFreeLimit                = true
      freeLimitExhaustionBehavior = "AutoPause"
      minCapacity                 = 0.5
      # The free-limit AutoPause offer rejects anything except its default 60.
      # General serverless capabilities list 15, but the live free-offer API does not allow it.
      autoPauseDelay                   = 60
      maxSizeBytes                     = 34359738368
      requestedBackupStorageRedundancy = "Local"
      zoneRedundant                    = false
    }
  }
  tags                   = local.tags
  response_export_values = ["properties", "sku"]
  lifecycle { prevent_destroy = true }
}

# SQL creates this child itself; manage its values without trying to recreate it.
resource "azapi_update_resource" "backup_retention" {
  type                   = "Microsoft.Sql/servers/databases/backupShortTermRetentionPolicies@2025-01-01"
  name                   = "default"
  parent_id              = azapi_resource.database.id
  body                   = { properties = { retentionDays = 7 } }
  response_export_values = ["properties.retentionDays"]
}

resource "azurerm_storage_account" "app" {
  name                            = var.storage_account_name
  resource_group_name             = azurerm_resource_group.app.name
  location                        = var.location
  account_tier                    = "Standard"
  account_replication_type        = "LRS"
  account_kind                    = "StorageV2"
  access_tier                     = "Hot"
  min_tls_version                 = "TLS1_2"
  https_traffic_only_enabled      = true
  shared_access_key_enabled       = false
  allow_nested_items_to_be_public = false
  default_to_oauth_authentication = true
  is_hns_enabled                  = false
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
  tags = local.tags
  lifecycle {
    prevent_destroy = true
    precondition {
      condition = (
        var.environment == "staging" ||
        (length(local.logical_backup_publisher_object_ids) == 0 && length(local.logical_photo_reader_object_ids) == 0)
      )
      error_message = "Logical recovery identities are staging-only until recovery drills pass."
    }
    precondition {
      condition = (
        length(local.logical_backup_publisher_object_ids) <= 1 &&
        length(local.logical_photo_reader_object_ids) <= 1
      )
      error_message = "Each logical recovery role supports at most one lifecycle-managed principal."
    }
    precondition {
      condition = length(setintersection(
        local.logical_backup_publisher_object_ids,
        local.logical_photo_reader_object_ids,
      )) == 0
      error_message = "Logical publisher and photo-reader principals must be distinct."
    }
    precondition {
      condition = length(setintersection(
        setunion(local.logical_backup_publisher_object_ids, local.logical_photo_reader_object_ids),
        setunion(local.runtime_object_ids, local.backup_object_ids, toset([lower(var.operator_object_id), local.terraform_runner_object_id])),
      )) == 0
      error_message = "Logical recovery principals must not reuse operator, Terraform runner, runtime, or legacy backup identities."
    }
  }
}

resource "azurerm_storage_container" "private" {
  for_each              = toset(["photos", "backups"])
  name                  = each.value
  storage_account_id    = azurerm_storage_account.app.id
  container_access_type = "private"
  depends_on            = [azurerm_role_assignment.operator]
  lifecycle { prevent_destroy = true }
}

resource "azurerm_role_assignment" "operator" {
  scope                = azurerm_storage_account.app.id
  role_definition_name = "Storage Blob Data Owner"
  principal_id         = var.operator_object_id
}

# Runtime can read/write photos, but cannot delete any blobs/versions or read backups.
resource "azurerm_role_definition" "photo_writer" {
  name        = "Sideyard ${var.environment} photo read write"
  scope       = azurerm_resource_group.app.id
  description = "Read and write private Sideyard photos without deletion or backup access."
  permissions {
    data_actions = [
      "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read",
      "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write"
    ]
  }
  assignable_scopes = [azurerm_resource_group.app.id]
}

resource "azurerm_role_assignment" "photos" {
  for_each           = local.runtime_object_ids
  scope              = "${azurerm_storage_account.app.id}/blobServices/default/containers/photos"
  role_definition_id = azurerm_role_definition.photo_writer.role_definition_resource_id
  principal_id       = each.value
  depends_on         = [azurerm_storage_container.private]
}

resource "azurerm_role_assignment" "backups" {
  for_each           = local.backup_object_ids
  scope              = "${azurerm_storage_account.app.id}/blobServices/default/containers/backups"
  role_definition_id = azurerm_role_definition.photo_writer.role_definition_resource_id
  principal_id       = each.value
  depends_on         = [azurerm_storage_container.private]
}

# Recovery publication is a separate trust domain from the legacy backup and
# application identities. Azure RBAC grants only the data operations; the
# authenticated recovery client enforces conditional creation and exact versions.
resource "azurerm_role_definition" "logical_backup_publisher" {
  for_each    = var.environment == "staging" ? toset(["staging"]) : toset([])
  name        = "Sideyard ${var.environment} logical backup publisher"
  scope       = azurerm_resource_group.app.id
  description = "Publish and verify logical recovery objects in the backups container without deletion."
  permissions {
    data_actions = [
      "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read",
      "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write"
    ]
  }
  assignable_scopes = [azurerm_resource_group.app.id]
}

resource "azurerm_role_assignment" "logical_backup_publisher" {
  for_each           = local.logical_backup_publisher_object_ids
  scope              = "${azurerm_storage_account.app.id}/blobServices/default/containers/backups"
  role_definition_id = azurerm_role_definition.logical_backup_publisher["staging"].role_definition_resource_id
  principal_id       = each.value
  principal_type     = "ServicePrincipal"
  depends_on         = [azurerm_storage_container.private]
}

resource "azurerm_role_definition" "logical_photo_reader" {
  for_each    = var.environment == "staging" ? toset(["staging"]) : toset([])
  name        = "Sideyard ${var.environment} logical photo reader"
  scope       = azurerm_resource_group.app.id
  description = "Read exact logical-recovery photo versions without write or delete access."
  permissions {
    data_actions = [
      "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read"
    ]
  }
  assignable_scopes = [azurerm_resource_group.app.id]
}

resource "azurerm_role_assignment" "logical_photo_reader" {
  for_each           = local.logical_photo_reader_object_ids
  scope              = "${azurerm_storage_account.app.id}/blobServices/default/containers/photos"
  role_definition_id = azurerm_role_definition.logical_photo_reader["staging"].role_definition_resource_id
  principal_id       = each.value
  principal_type     = "ServicePrincipal"
  depends_on         = [azurerm_storage_container.private]
}

# Export keys are immutable. This age rule applies ONLY to encrypted database
# exports and their completion records, never photo/state blobs.
resource "azurerm_storage_management_policy" "exports" {
  storage_account_id = azurerm_storage_account.app.id
  rule {
    name    = "seven-day-encrypted-exports"
    enabled = true
    filters {
      prefix_match = ["backups/postgres/", "backups/logical-completions/"]
      blob_types   = ["blockBlob"]
    }
    actions {
      base_blob { delete_after_days_since_modification_greater_than = 7 }
      version { delete_after_days_since_creation = 7 }
      snapshot { delete_after_days_since_creation_greater_than = 7 }
    }
  }
  # Components remain one day longer than their completion record. Therefore a
  # retained completion marker can never outlive the versions it references.
  rule {
    name    = "eight-day-logical-components"
    enabled = true
    filters {
      prefix_match = ["backups/logical-components/"]
      blob_types   = ["blockBlob"]
    }
    actions {
      base_blob { delete_after_days_since_modification_greater_than = 8 }
      version { delete_after_days_since_creation = 8 }
      snapshot { delete_after_days_since_creation_greater_than = 8 }
    }
  }
}

resource "azurerm_management_lock" "database" {
  name       = "preserve-database"
  scope      = azapi_resource.database.id
  lock_level = "CanNotDelete"
}

resource "azurerm_management_lock" "storage" {
  name       = "preserve-storage"
  scope      = azurerm_storage_account.app.id
  lock_level = "CanNotDelete"
}

resource "azurerm_monitor_action_group" "owner" {
  name                = "sideyard-${var.environment}-owner"
  resource_group_name = azurerm_resource_group.app.name
  short_name          = var.environment == "production" ? "sideyardprod" : "sideyardstg"
  email_receiver {
    name                    = "owner"
    email_address           = "wmichelin@gmail.com"
    use_common_alert_schema = true
  }
  tags = local.tags
}

resource "azurerm_monitor_metric_alert" "free_compute" {
  for_each            = { warning = 30000, critical = 10000 }
  name                = "sideyard-${var.environment}-compute-${each.key}"
  resource_group_name = azurerm_resource_group.app.name
  scopes              = [azapi_resource.database.id]
  enabled             = true
  severity            = each.key == "critical" ? 1 : 2
  frequency           = "PT15M"
  window_size         = "PT1H"
  description         = "Sideyard ${var.environment} free SQL compute remaining is low; no paid overages enabled."
  criteria {
    metric_namespace = "Microsoft.Sql/servers/databases"
    metric_name      = "free_amount_remaining"
    aggregation      = "Minimum"
    operator         = "LessThan"
    threshold        = each.value
  }
  action { action_group_id = azurerm_monitor_action_group.owner.id }
  tags = local.tags
}

output "resources" {
  value = {
    environment                                 = var.environment
    resource_group                              = azurerm_resource_group.app.name
    sql_server                                  = azurerm_mssql_server.app.fully_qualified_domain_name
    database_id                                 = azapi_resource.database.id
    storage_account                             = azurerm_storage_account.app.name
    storage_account_id                          = azurerm_storage_account.app.id
    blob_endpoint                               = azurerm_storage_account.app.primary_blob_endpoint
    backups_container_id                        = "${azurerm_storage_account.app.id}/blobServices/default/containers/backups"
    photos_container_id                         = "${azurerm_storage_account.app.id}/blobServices/default/containers/photos"
    terraform_runner_object_id                  = data.azurerm_client_config.current.object_id
    logical_backup_publisher_role_definition_id = try(azurerm_role_definition.logical_backup_publisher["staging"].role_definition_resource_id, null)
    logical_photo_reader_role_definition_id     = try(azurerm_role_definition.logical_photo_reader["staging"].role_definition_resource_id, null)
    backup_retention_days                       = azapi_update_resource.backup_retention.output.properties.retentionDays
    action_group_id                             = azurerm_monitor_action_group.owner.id
  }
}
