mock_provider "azurerm" {
  override_data {
    target = data.azurerm_client_config.current
    values = {
      tenant_id = "22222222-2222-2222-2222-222222222222"
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
