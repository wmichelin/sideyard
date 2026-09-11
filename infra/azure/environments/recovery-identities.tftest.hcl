mock_provider "azurerm" {
  override_data {
    target = data.azurerm_client_config.current
    values = {
      tenant_id = "22222222-2222-2222-2222-222222222222"
    }
  }

  override_resource {
    target = azurerm_monitor_action_group.owner
    values = {
      id = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/sideyard-staging/providers/Microsoft.Insights/actionGroups/sideyard-staging-owner"
    }
  }
}

mock_provider "azapi" {}

variables {
  subscription_id      = "11111111-1111-1111-1111-111111111111"
  operator_object_id   = "22222222-2222-2222-2222-222222222222"
  operator_login       = "owner@example.com"
  environment          = "staging"
  sql_server_name      = "sideyard-staging-test"
  storage_account_name = "sideyardstagingtest"
  allowed_ipv4         = ["192.0.2.1"]
}

run "staging_free_foundation_contract" {
  command = plan

  assert {
    condition = (
      azapi_resource.database.body.sku.name == "GP_S_Gen5" &&
      azapi_resource.database.body.sku.capacity == 1 &&
      azapi_resource.database.body.properties.useFreeLimit == true &&
      azapi_resource.database.body.properties.freeLimitExhaustionBehavior == "AutoPause" &&
      azapi_resource.database.body.properties.minCapacity == 0.5 &&
      azapi_resource.database.body.properties.autoPauseDelay == 60 &&
      azapi_resource.database.body.properties.maxSizeBytes == 34359738368 &&
      azapi_resource.database.body.properties.requestedBackupStorageRedundancy == "Local" &&
      azapi_update_resource.backup_retention.body.properties.retentionDays == 7
    )
    error_message = "Staging SQL must retain the bounded free-offer and seven-day backup contract."
  }

  assert {
    condition = (
      azurerm_storage_account.app.account_tier == "Standard" &&
      azurerm_storage_account.app.account_replication_type == "LRS" &&
      azurerm_storage_account.app.access_tier == "Hot" &&
      azurerm_storage_account.app.shared_access_key_enabled == false &&
      azurerm_storage_account.app.default_to_oauth_authentication == true &&
      azurerm_storage_account.app.network_rules[0].default_action == "Deny" &&
      azurerm_storage_account.app.blob_properties[0].versioning_enabled == true &&
      azurerm_storage_account.app.blob_properties[0].delete_retention_policy[0].days == 7 &&
      azurerm_storage_account.app.blob_properties[0].container_delete_retention_policy[0].days == 7 &&
      alltrue([for container in azurerm_storage_container.private : container.container_access_type == "private"])
    )
    error_message = "Staging Blob storage must remain private, keyless, versioned and retained."
  }

  assert {
    condition     = length(azurerm_monitor_metric_alert.free_compute) == 2
    error_message = "Staging must retain warning and critical free-compute alerts."
  }

  assert {
    condition     = alltrue([for alert in azurerm_monitor_metric_alert.free_compute : alert.enabled])
    error_message = "Both free-compute alerts must remain enabled."
  }

  assert {
    condition = (
      toset([for alert in azurerm_monitor_metric_alert.free_compute : alert.criteria[0].metric_name]) == toset(["free_amount_remaining"]) &&
      toset([for alert in azurerm_monitor_metric_alert.free_compute : alert.criteria[0].threshold]) == toset([10000, 30000])
    )
    error_message = "Free-compute alerts must retain their metric and warning thresholds."
  }

  assert {
    condition     = alltrue([for alert in azurerm_monitor_metric_alert.free_compute : length(alert.action) == 1])
    error_message = "Each free-compute alert must retain one action-group binding."
  }

  assert {
    condition = (
      one([for rule in azurerm_storage_management_policy.exports.rule : rule if rule.name == "seven-day-encrypted-exports"]).enabled &&
      toset(one(one([for rule in azurerm_storage_management_policy.exports.rule : rule if rule.name == "seven-day-encrypted-exports"]).filters).prefix_match) == toset(["backups/postgres/", "backups/logical-completions/"]) &&
      one([for rule in azurerm_storage_management_policy.exports.rule : rule if rule.name == "eight-day-logical-components"]).enabled &&
      toset(one(one([for rule in azurerm_storage_management_policy.exports.rule : rule if rule.name == "eight-day-logical-components"]).filters).prefix_match) == toset(["backups/logical-components/"])
    )
    error_message = "Logical backup lifecycle prefixes and windows must remain distinct."
  }
}

run "distinct_staging_identities" {
  command = plan

  variables {
    runtime_object_ids                  = ["33333333-3333-3333-3333-333333333333"]
    backup_object_ids                   = ["44444444-4444-4444-4444-444444444444"]
    logical_backup_publisher_object_ids = ["55555555-5555-5555-5555-555555555555"]
    logical_photo_reader_object_ids     = ["66666666-6666-6666-6666-666666666666"]
  }

  assert {
    condition     = length(azurerm_role_definition.logical_backup_publisher) == 1
    error_message = "Staging must define the logical publisher role."
  }
  assert {
    condition     = length(azurerm_role_definition.logical_photo_reader) == 1
    error_message = "Staging must define the logical photo-reader role."
  }
}

run "mixed_case_recovery_overlap_fails" {
  command = plan

  variables {
    logical_backup_publisher_object_ids = ["AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA"]
    logical_photo_reader_object_ids     = ["aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"]
  }

  expect_failures = [azurerm_storage_account.app]
}

run "mixed_case_runtime_overlap_fails" {
  command = plan

  variables {
    runtime_object_ids                  = ["BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB"]
    logical_backup_publisher_object_ids = ["bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"]
  }

  expect_failures = [azurerm_storage_account.app]
}

run "mixed_case_legacy_backup_overlap_fails" {
  command = plan

  variables {
    backup_object_ids                   = ["DDDDDDDD-DDDD-DDDD-DDDD-DDDDDDDDDDDD"]
    logical_backup_publisher_object_ids = ["dddddddd-dddd-dddd-dddd-dddddddddddd"]
  }

  expect_failures = [azurerm_storage_account.app]
}

run "mixed_case_operator_overlap_fails" {
  command = plan

  variables {
    operator_object_id              = "CCCCCCCC-CCCC-CCCC-CCCC-CCCCCCCCCCCC"
    logical_photo_reader_object_ids = ["cccccccc-cccc-cccc-cccc-cccccccccccc"]
  }

  expect_failures = [azurerm_storage_account.app]
}

run "production_has_no_recovery_roles" {
  command = plan

  variables {
    environment          = "production"
    sql_server_name      = "sideyard-production-test"
    storage_account_name = "sideyardproductiontest"
  }

  assert {
    condition     = length(azurerm_role_definition.logical_backup_publisher) == 0
    error_message = "Production must not define a logical publisher role."
  }
  assert {
    condition     = length(azurerm_role_definition.logical_photo_reader) == 0
    error_message = "Production must not define a logical photo-reader role."
  }
}

run "production_recovery_assignment_fails" {
  command = plan

  variables {
    environment                     = "production"
    sql_server_name                 = "sideyard-production-test"
    storage_account_name            = "sideyardproductiontest"
    logical_photo_reader_object_ids = ["eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"]
  }

  expect_failures = [azurerm_storage_account.app]
}

run "multiple_publishers_fail" {
  command = plan

  variables {
    logical_backup_publisher_object_ids = [
      "11111111-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
      "22222222-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
    ]
  }

  expect_failures = [azurerm_storage_account.app]
}

run "multiple_photo_readers_fail" {
  command = plan

  variables {
    logical_photo_reader_object_ids = [
      "33333333-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
      "44444444-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
    ]
  }

  expect_failures = [azurerm_storage_account.app]
}
