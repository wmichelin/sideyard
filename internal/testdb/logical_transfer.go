package testdb

import (
	"context"
	"fmt"

	"github.com/wmichelin/clubdue/internal/database"
)

// SeedLogicalTransfer inserts one connected, nonempty row into every table in
// the selected transfer profile. It is deterministic and only for disposable
// real-engine tests.
func SeedLogicalTransfer(ctx context.Context, pool database.Pool, includeCurrent bool) error {
	base := postgresLogicalFixture
	current := postgresCurrentLogicalFixture
	if pool.Dialect() == database.SQLServer {
		if !includeCurrent {
			return fmt.Errorf("historical logical fixture requires PostgreSQL")
		}
		base = sqlServerLogicalFixture
		current = sqlServerCurrentLogicalFixture
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin logical transfer fixture: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err := tx.Exec(ctx, base); err != nil {
		return fmt.Errorf("seed logical transfer base fixture: %w", err)
	}
	if includeCurrent {
		if _, err := tx.Exec(ctx, current); err != nil {
			return fmt.Errorf("seed logical transfer current fixture: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit logical transfer fixture: %w", err)
	}
	return nil
}

const postgresLogicalFixture = `
INSERT INTO interest_signups(id,name,email,business,trade,message,created_at) VALUES
('f1000000-0000-4000-8000-000000000004','Zoë & Sons','logical-transfer@example.test','All Values','landscaping','Snowman ☃ &<>','2035-09-02T12:00:00.123456Z');
INSERT INTO interest_notifications(signup_id,attempts,next_attempt_at,sent_at,provider_message_id) VALUES
('f1000000-0000-4000-8000-000000000004',2,'2035-09-02T12:01:00.123456Z','2035-09-02T12:02:00.123456Z','provider-fixture');
INSERT INTO shops(id,name,slug,created_at,kind,expires_at,demo_mutations_used,demo_import_rows_used,demo_import_occurrences_used) VALUES
('f1000000-0000-4000-8000-000000000001','Logical Transfer','logical-transfer','2035-09-02T12:00:00.123456Z','customer',NULL,3,4,5);
INSERT INTO users(id,shop_id,email,password_hash,role,created_at) VALUES
('f1000000-0000-4000-8000-000000000002','f1000000-0000-4000-8000-000000000001','owner.logical@example.test','hash:fixture','owner','2035-09-02T12:00:01.123456Z');
INSERT INTO sessions(id,user_id,token_hash,expires_at,created_at) VALUES
('f1000000-0000-4000-8000-000000000003','f1000000-0000-4000-8000-000000000002',repeat('a',64),'2036-09-02T12:00:00.123456Z','2035-09-02T12:00:02.123456Z');
INSERT INTO owner_provision_events(id,user_id,action,created_at) VALUES
('f1000000-0000-4000-8000-000000000005','f1000000-0000-4000-8000-000000000002','recovered','2035-09-02T12:00:03.123456Z');
INSERT INTO pilot_activation_events(id,shop_id,event_type,occurred_at) VALUES
('f1000000-0000-4000-8000-000000000006','f1000000-0000-4000-8000-000000000001','returning_use','2035-09-02T12:00:04.123456Z');
INSERT INTO customers(id,shop_id,external_id,name,email,phone,created_at,updated_at) VALUES
('f1000000-0000-4000-8000-000000000010','f1000000-0000-4000-8000-000000000001','customer-fixture','María 李 🚜','customer@example.test','+1 614 555 0100','2035-09-02T12:00:05.123456Z','2035-09-02T12:00:06.123456Z'),
('f1000000-0000-4000-8000-000000000031','f1000000-0000-4000-8000-000000000001',NULL,'Null and empty',NULL,'','2035-09-02T12:00:05.123456Z','2035-09-02T12:00:06.123456Z'),
('f1000000-0000-4000-8000-000000000032','f1000000-0000-4000-8000-000000000001','','Empty strings','','','2035-09-02T12:00:05.123456Z','2035-09-02T12:00:06.123456Z');
INSERT INTO properties(id,shop_id,customer_id,external_id,label,address_line1,address_line2,city,state,postal_code,created_at,updated_at,address_revision) VALUES
('f1000000-0000-4000-8000-000000000011','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000010','property-fixture','North lawn','1 Fixture Way','Unit Ω','Columbus','OH','43215','2035-09-02T12:00:07.123456Z','2035-09-02T12:00:08.123456Z',2);
INSERT INTO equipment(id,shop_id,property_id,external_id,kind,make,model,serial_number,installed_on,created_at,location,warranty_until,internal_notes,revision) VALUES
('f1000000-0000-4000-8000-000000000012','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000011','equipment-fixture','heat pump','Märke','M-1','SN-雪','2030-01-02','2035-09-02T12:00:09.123456Z','Attic','2040-01-02','Exact notes  ',3);
INSERT INTO customer_vehicles(id,shop_id,customer_id,name,make,model,model_year,identifier,internal_notes,revision) VALUES
('f1000000-0000-4000-8000-000000000013','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000010','Work truck','Ford','F-150',2029,'VIN-FIXTURE','Cab note',2);
INSERT INTO property_details(shop_id,property_id,access_notes,revision) VALUES
('f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000011','Gate at rear',4);
INSERT INTO property_measurements(id,shop_id,property_id,kind,zone,value,unit,source,measured_on,estimated,recorded_by,recorded_at) VALUES
('f1000000-0000-4000-8000-000000000014','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000011','turf','North',12345.678,'sq_ft','laser','2035-08-31',false,'Owner','2035-09-02T12:00:10.123456Z');
INSERT INTO property_notes(id,shop_id,property_id,submission_key,body,occurred_on,recorded_by,recorded_at) VALUES
('f1000000-0000-4000-8000-000000000015','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000011','11111111111111111111111111111111','Watch the roses','2035-09-01','Owner','2035-09-02T12:00:11.123456Z');
INSERT INTO property_photos(id,shop_id,property_id,submission_key,content_hash,caption,category,content_type,image_data,recorded_by,recorded_at,deleted_at) VALUES
('f1000000-0000-4000-8000-000000000016','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000011','22222222222222222222222222222222','5240672d7b51756b829ad0ef8d9468b7a078afa2f410484fd3892dab47becb72','Before & after','before','image/png',decode('0080ff','hex'),'Owner','2035-09-02T12:00:12.123456Z',NULL);
INSERT INTO asset_notes(id,shop_id,vehicle_id,equipment_id,submission_key,body,occurred_on,occurred_at,event_timezone,recorded_by,recorded_at) VALUES
('f1000000-0000-4000-8000-000000000017','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000013',NULL,'33333333333333333333333333333333','Changed filter','2035-09-01','2035-09-01T15:30:00.123456Z','America/New_York','Owner','2035-09-02T12:00:13.123456Z');
INSERT INTO business_settings(shop_id,primary_trade,trades,revision) VALUES
('f1000000-0000-4000-8000-000000000001','landscaping',ARRAY['landscaping','hvac',''],2);
INSERT INTO business_services(id,shop_id,preset_key,preset_version,name,description,enabled,revision) VALUES
('f1000000-0000-4000-8000-000000000027','f1000000-0000-4000-8000-000000000001','fixture-service',1,'Fixture service','Description ☃',false,3);
INSERT INTO plan_templates(id,shop_id,name,description,price_cents,billing_interval,visits_per_year,visit_windows,created_at,term_months) VALUES
('f1000000-0000-4000-8000-000000000018','f1000000-0000-4000-8000-000000000001','Five-step plan','Flexible billing',250000,'annual',5,ARRAY['spring','summer','fall'],'2035-09-02T12:00:14.123456Z',12);
INSERT INTO agreements(id,shop_id,customer_id,property_id,plan_template_id,external_id,status,anniversary_date,visits_entitled,visits_used,price_cents,notes,created_at,updated_at,plan_snapshot_name,plan_snapshot_description,plan_snapshot_billing_interval,plan_snapshot_term_months,plan_snapshot_visits_per_year,plan_snapshot_visit_windows) VALUES
('f1000000-0000-4000-8000-000000000019','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000010','f1000000-0000-4000-8000-000000000011','f1000000-0000-4000-8000-000000000018','agreement-fixture','active','2036-01-01',5,1,250000,'Pay upfront or per visit','2035-09-02T12:00:15.123456Z','2035-09-02T12:00:16.123456Z','Five-step plan','Flexible billing','annual',12,5,ARRAY['spring','summer','fall']);
INSERT INTO visit_dues(id,shop_id,agreement_id,season,due_year,status,scheduled_at,completed_at,notes,created_at) VALUES
('f1000000-0000-4000-8000-000000000020','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000019','fall',2035,'completed','2035-09-01T14:00:00.123456Z','2035-09-01T15:00:00.123456Z','Done','2035-08-01T12:00:00.123456Z');
INSERT INTO service_schedules(id,shop_id,agreement_id,external_id,service_name,cadence_unit,cadence_interval,starts_on,ends_on,generated_through,active,paused_at,counts_toward_entitlement,created_at,updated_at) VALUES
('f1000000-0000-4000-8000-000000000021','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000019','schedule-fixture','Treatment','week',2,'2035-01-01','2035-12-31','2035-09-01',true,NULL,true,'2035-01-01T12:00:00.123456Z','2035-09-02T12:00:17.123456Z');
INSERT INTO service_occurrences(id,shop_id,schedule_id,due_on,status,completed_at,notes,created_at,updated_at,scheduled_on,schedule_revision) VALUES
('f1000000-0000-4000-8000-000000000022','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000021','2035-09-01','completed','2035-09-01T15:00:00.123456Z','Applied','2035-08-01T12:00:00.123456Z','2035-09-01T15:00:01.123456Z','2035-09-01',2);
INSERT INTO visit_notes(id,shop_id,occurrence_id,seasonal_visit_id,submission_key,body,occurred_on,occurred_at,event_timezone,recorded_by,recorded_at) VALUES
('f1000000-0000-4000-8000-000000000023','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000022',NULL,'44444444444444444444444444444444','Application complete','2035-09-01','2035-09-01T15:00:00.123456Z','America/New_York','Owner','2035-09-02T12:00:18.123456Z');
INSERT INTO renewal_links(id,shop_id,agreement_id,token_hash,expires_at,created_at) VALUES
('f1000000-0000-4000-8000-000000000024','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000019','renewal-token-fixture','2036-01-01T12:00:00.123456Z','2035-09-02T12:00:19.123456Z');
INSERT INTO renewal_tasks(id,shop_id,agreement_id,milestone_days,status,notes,updated_at,cycle_start_date,due_at) VALUES
('f1000000-0000-4000-8000-000000000025','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000019',90,'contacted','Called','2035-09-02T12:00:20.123456Z','2036-01-01','2035-10-03');
INSERT INTO renewal_digest_outbox(id,shop_id,digest_date,task_ids,status,created_at,sent_at) VALUES
('f1000000-0000-4000-8000-000000000026','f1000000-0000-4000-8000-000000000001','2035-09-02',ARRAY['f1000000-0000-4000-8000-000000000025'::uuid],'sent','2035-09-02T12:00:21.123456Z','2035-09-02T12:00:22.123456Z');
INSERT INTO demo_import_previews(token_hash,shop_id,csv_data,row_count,projected_occurrences,projection_date,expires_at,created_at) VALUES
('preview-fixture','f1000000-0000-4000-8000-000000000001',decode('612c620a','hex'),1,5,'2035-09-02','2035-09-03T12:00:00.123456Z','2035-09-02T12:00:23.123456Z');
INSERT INTO import_rows(id,shop_id,source,external_id,entity_type,entity_id,content_hash,imported_at) VALUES
('f1000000-0000-4000-8000-000000000028','f1000000-0000-4000-8000-000000000001','fixture','row-1','customer','f1000000-0000-4000-8000-000000000010',repeat('f',64),'2035-09-02T12:00:24.123456Z');
`

const postgresCurrentLogicalFixture = `
UPDATE property_photos SET object_key=repeat('d',64),object_version='version-1',object_pending=false
WHERE id='f1000000-0000-4000-8000-000000000016';
INSERT INTO property_photos(id,shop_id,property_id,submission_key,content_hash,caption,category,content_type,image_data,recorded_by,recorded_at,deleted_at,object_key,object_version,object_pending) VALUES
('f1000000-0000-4000-8000-000000000033','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000011','66666666666666666666666666666666','039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81','Pending inline copy','general','image/png',decode('010203','hex'),'Owner','2035-09-02T12:00:24.123456Z',NULL,repeat('a',64),NULL,true),
('f1000000-0000-4000-8000-000000000036','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000011','77777777777777777777777777777777','787c798e39a5bc1910355bae6d0cd87a36b2e10fd0202a83e3bb6b005da83472','Legacy backfill','general','image/png',decode('040506','hex'),'Owner','2035-09-02T12:00:24.123456Z',NULL,repeat('e',64),NULL,false),
('f1000000-0000-4000-8000-000000000037','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000011','88888888888888888888888888888888','5a8814ae66ff07179d2c22381da6221f6fe754e6175c47d7d87846080f0a9715','','general','image/png',NULL,'Owner','2035-09-02T12:00:24.123456Z','2035-09-03T12:00:24.123456Z',repeat('f',64),'version-linked-purged',false);
INSERT INTO property_photo_objects(object_key,shop_id,photo_id,content_hash,content_type,byte_length,version_id,created_at,retired_at,purged_at) VALUES
(repeat('a',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000033','039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81','image/png',3,NULL,'2035-09-02T12:00:25.123456Z',NULL,NULL),
(repeat('b',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000034','d798d1fac6bd4bb1c11f50312760351013379a0ab6f0a8c0af8a506b96b2525a','image/jpeg',2,'version-retired','2035-09-02T12:00:25.123456Z','2035-09-03T12:00:25.123456Z',NULL),
(repeat('c',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000035','5a8814ae66ff07179d2c22381da6221f6fe754e6175c47d7d87846080f0a9715','image/png',2,'version-purged','2035-09-02T12:00:25.123456Z','2035-09-03T12:00:25.123456Z','2035-09-10T12:00:25.123456Z'),
(repeat('d',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000016','5240672d7b51756b829ad0ef8d9468b7a078afa2f410484fd3892dab47becb72','image/png',3,'version-1','2035-09-02T12:00:25.123456Z',NULL,NULL),
(repeat('e',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000036','787c798e39a5bc1910355bae6d0cd87a36b2e10fd0202a83e3bb6b005da83472','image/png',3,NULL,'2035-09-02T12:00:25.123456Z',NULL,NULL),
(repeat('f',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000037','5a8814ae66ff07179d2c22381da6221f6fe754e6175c47d7d87846080f0a9715','image/png',2,'version-linked-purged','2035-09-02T12:00:25.123456Z','2035-09-03T12:00:25.123456Z','2035-09-10T12:00:25.123456Z');
INSERT INTO jobs(id,shop_id,customer_id,property_id,submission_key,creation_fingerprint,kind,title,details,scheduled_on,status,completed_at,revision,created_at,updated_at) VALUES
('f1000000-0000-4000-8000-000000000030','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000010','f1000000-0000-4000-8000-000000000011','55555555555555555555555555555555',repeat('e',64),'survey','Site survey','{"zones":["north","南"],"empty":""}','2035-09-03','planned',NULL,1,'2035-09-02T12:00:26.123456Z','2035-09-02T12:00:27.123456Z');
INSERT INTO job_events(shop_id,job_id,revision,action,snapshot,recorded_by,recorded_at) VALUES
('f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000030',1,'created','{"empty":"","tags":["alpha","β"]}'::jsonb,'Owner','2035-09-02T12:00:28.123456Z');
`

const sqlServerLogicalFixture = `
DECLARE @shop uniqueidentifier='f1000000-0000-4000-8000-000000000001';
DECLARE @user uniqueidentifier='f1000000-0000-4000-8000-000000000002';
DECLARE @customer uniqueidentifier='f1000000-0000-4000-8000-000000000010';
DECLARE @property uniqueidentifier='f1000000-0000-4000-8000-000000000011';
DECLARE @equipment uniqueidentifier='f1000000-0000-4000-8000-000000000012';
DECLARE @vehicle uniqueidentifier='f1000000-0000-4000-8000-000000000013';
DECLARE @photo uniqueidentifier='f1000000-0000-4000-8000-000000000016';
DECLARE @agreement uniqueidentifier='f1000000-0000-4000-8000-000000000019';
DECLARE @schedule uniqueidentifier='f1000000-0000-4000-8000-000000000021';
DECLARE @occurrence uniqueidentifier='f1000000-0000-4000-8000-000000000022';
DECLARE @task uniqueidentifier='f1000000-0000-4000-8000-000000000025';
INSERT INTO dbo.interest_signups(id,name,email,business,trade,message,created_at) VALUES
('f1000000-0000-4000-8000-000000000004',N'Zoë & Sons',N'logical-transfer@example.test',N'All Values',N'landscaping',N'Snowman ☃ &<>','2035-09-02T12:00:00.123456');
INSERT INTO dbo.interest_notifications(signup_id,attempts,next_attempt_at,sent_at,provider_message_id) VALUES
('f1000000-0000-4000-8000-000000000004',2,'2035-09-02T12:01:00.123456','2035-09-02T12:02:00.123456',N'provider-fixture');
INSERT INTO dbo.shops(id,name,slug,created_at,kind,expires_at,demo_mutations_used,demo_import_rows_used,demo_import_occurrences_used) VALUES
(@shop,N'Logical Transfer',N'logical-transfer','2035-09-02T12:00:00.123456',N'customer',NULL,3,4,5);
INSERT INTO dbo.users(id,shop_id,email,email_normalized,password_hash,role,created_at) VALUES
(@user,@shop,N'owner.logical@example.test',N'owner.logical@example.test',N'hash:fixture',N'owner','2035-09-02T12:00:01.123456');
INSERT INTO dbo.sessions(id,user_id,token_hash,expires_at,created_at) VALUES
('f1000000-0000-4000-8000-000000000003',@user,REPLICATE('a',64),'2036-09-02T12:00:00.123456','2035-09-02T12:00:02.123456');
INSERT INTO dbo.owner_provision_events(id,user_id,action,created_at) VALUES
('f1000000-0000-4000-8000-000000000005',@user,N'recovered','2035-09-02T12:00:03.123456');
INSERT INTO dbo.pilot_activation_events(id,shop_id,event_type,occurred_at) VALUES
('f1000000-0000-4000-8000-000000000006',@shop,N'returning_use','2035-09-02T12:00:04.123456');
INSERT INTO dbo.customers(id,shop_id,external_id,name,email,phone,created_at,updated_at) VALUES
(@customer,@shop,N'customer-fixture',N'María 李 🚜',N'customer@example.test',N'+1 614 555 0100','2035-09-02T12:00:05.123456','2035-09-02T12:00:06.123456'),
('f1000000-0000-4000-8000-000000000031',@shop,NULL,N'Null and empty',NULL,N'','2035-09-02T12:00:05.123456','2035-09-02T12:00:06.123456'),
('f1000000-0000-4000-8000-000000000032',@shop,N'',N'Empty strings',N'',N'','2035-09-02T12:00:05.123456','2035-09-02T12:00:06.123456');
INSERT INTO dbo.properties(id,shop_id,customer_id,external_id,label,address_line1,address_line2,city,state,postal_code,created_at,updated_at,address_revision) VALUES
(@property,@shop,@customer,N'property-fixture',N'North lawn',N'1 Fixture Way',N'Unit Ω',N'Columbus',N'OH',N'43215','2035-09-02T12:00:07.123456','2035-09-02T12:00:08.123456',2);
INSERT INTO dbo.equipment(id,shop_id,property_id,external_id,kind,make,model,serial_number,installed_on,created_at,location,warranty_until,internal_notes,revision) VALUES
(@equipment,@shop,@property,N'equipment-fixture',N'heat pump',N'Märke',N'M-1',N'SN-雪','2030-01-02','2035-09-02T12:00:09.123456',N'Attic','2040-01-02',N'Exact notes  ',3);
INSERT INTO dbo.customer_vehicles(id,shop_id,customer_id,name,make,model,model_year,identifier,internal_notes,revision) VALUES
(@vehicle,@shop,@customer,N'Work truck',N'Ford',N'F-150',2029,N'VIN-FIXTURE',N'Cab note',2);
INSERT INTO dbo.property_details(shop_id,property_id,access_notes,revision) VALUES(@shop,@property,N'Gate at rear',4);
INSERT INTO dbo.property_measurements(id,shop_id,property_id,kind,zone,value,unit,source,measured_on,estimated,recorded_by,recorded_at) VALUES
('f1000000-0000-4000-8000-000000000014',@shop,@property,N'turf',N'North',12345.678,N'sq_ft',N'laser','2035-08-31',0,N'Owner','2035-09-02T12:00:10.123456');
INSERT INTO dbo.property_notes(id,shop_id,property_id,submission_key,body,occurred_on,recorded_by,recorded_at) VALUES
('f1000000-0000-4000-8000-000000000015',@shop,@property,N'11111111111111111111111111111111',N'Watch the roses','2035-09-01',N'Owner','2035-09-02T12:00:11.123456');
INSERT INTO dbo.property_photos(id,shop_id,property_id,submission_key,content_hash,caption,category,content_type,image_data,recorded_by,recorded_at,deleted_at,object_key,object_version,object_pending) VALUES
(@photo,@shop,@property,N'22222222222222222222222222222222',N'5240672d7b51756b829ad0ef8d9468b7a078afa2f410484fd3892dab47becb72',N'Before & after',N'before',N'image/png',0x0080ff,N'Owner','2035-09-02T12:00:12.123456',NULL,REPLICATE(N'd',64),N'version-1',0);
INSERT INTO dbo.asset_notes(id,shop_id,vehicle_id,equipment_id,submission_key,body,occurred_on,occurred_at,event_timezone,recorded_by,recorded_at) VALUES
('f1000000-0000-4000-8000-000000000017',@shop,@vehicle,NULL,N'33333333333333333333333333333333',N'Changed filter','2035-09-01','2035-09-01T15:30:00.123456',N'America/New_York',N'Owner','2035-09-02T12:00:13.123456');
INSERT INTO dbo.business_settings(shop_id,primary_trade,trades,revision) VALUES(@shop,N'landscaping',N'["landscaping","hvac",""]',2);
INSERT INTO dbo.business_services(id,shop_id,preset_key,preset_version,name,description,enabled,revision) VALUES
('f1000000-0000-4000-8000-000000000027',@shop,N'fixture-service',1,N'Fixture service',N'Description ☃',0,3);
INSERT INTO dbo.plan_templates(id,shop_id,name,description,price_cents,billing_interval,visits_per_year,visit_windows,created_at,term_months) VALUES
('f1000000-0000-4000-8000-000000000018',@shop,N'Five-step plan',N'Flexible billing',250000,N'annual',5,N'["spring","summer","fall"]','2035-09-02T12:00:14.123456',12);
INSERT INTO dbo.agreements(id,shop_id,customer_id,property_id,plan_template_id,external_id,status,anniversary_date,visits_entitled,visits_used,price_cents,notes,created_at,updated_at,plan_snapshot_name,plan_snapshot_description,plan_snapshot_billing_interval,plan_snapshot_term_months,plan_snapshot_visits_per_year,plan_snapshot_visit_windows) VALUES
(@agreement,@shop,@customer,@property,'f1000000-0000-4000-8000-000000000018',N'agreement-fixture',N'active','2036-01-01',5,1,250000,N'Pay upfront or per visit','2035-09-02T12:00:15.123456','2035-09-02T12:00:16.123456',N'Five-step plan',N'Flexible billing',N'annual',12,5,N'["spring","summer","fall"]');
INSERT INTO dbo.visit_dues(id,shop_id,agreement_id,season,due_year,status,scheduled_at,completed_at,notes,created_at) VALUES
('f1000000-0000-4000-8000-000000000020',@shop,@agreement,N'fall',2035,N'completed','2035-09-01T14:00:00.123456','2035-09-01T15:00:00.123456',N'Done','2035-08-01T12:00:00.123456');
INSERT INTO dbo.service_schedules(id,shop_id,agreement_id,external_id,service_name,cadence_unit,cadence_interval,starts_on,ends_on,generated_through,active,paused_at,counts_toward_entitlement,created_at,updated_at) VALUES
(@schedule,@shop,@agreement,N'schedule-fixture',N'Treatment',N'week',2,'2035-01-01','2035-12-31','2035-09-01',1,NULL,1,'2035-01-01T12:00:00.123456','2035-09-02T12:00:17.123456');
INSERT INTO dbo.service_occurrences(id,shop_id,schedule_id,due_on,status,completed_at,notes,created_at,updated_at,scheduled_on,schedule_revision) VALUES
(@occurrence,@shop,@schedule,'2035-09-01',N'completed','2035-09-01T15:00:00.123456',N'Applied','2035-08-01T12:00:00.123456','2035-09-01T15:00:01.123456','2035-09-01',2);
INSERT INTO dbo.visit_notes(id,shop_id,occurrence_id,seasonal_visit_id,submission_key,body,occurred_on,occurred_at,event_timezone,recorded_by,recorded_at) VALUES
('f1000000-0000-4000-8000-000000000023',@shop,@occurrence,NULL,N'44444444444444444444444444444444',N'Application complete','2035-09-01','2035-09-01T15:00:00.123456',N'America/New_York',N'Owner','2035-09-02T12:00:18.123456');
INSERT INTO dbo.renewal_links(id,shop_id,agreement_id,token_hash,expires_at,created_at) VALUES
('f1000000-0000-4000-8000-000000000024',@shop,@agreement,N'renewal-token-fixture','2036-01-01T12:00:00.123456','2035-09-02T12:00:19.123456');
INSERT INTO dbo.renewal_tasks(id,shop_id,agreement_id,milestone_days,status,notes,updated_at,cycle_start_date,due_at) VALUES
(@task,@shop,@agreement,90,N'contacted',N'Called','2035-09-02T12:00:20.123456','2036-01-01','2035-10-03');
INSERT INTO dbo.renewal_digest_outbox(id,shop_id,digest_date,task_ids,status,created_at,sent_at) VALUES
('f1000000-0000-4000-8000-000000000026',@shop,'2035-09-02',N'["f1000000-0000-4000-8000-000000000025"]',N'sent','2035-09-02T12:00:21.123456','2035-09-02T12:00:22.123456');
INSERT INTO dbo.demo_import_previews(token_hash,shop_id,csv_data,row_count,projected_occurrences,projection_date,expires_at,created_at) VALUES
(N'preview-fixture',@shop,0x612c620a,1,5,'2035-09-02','2035-09-03T12:00:00.123456','2035-09-02T12:00:23.123456');
INSERT INTO dbo.import_rows(id,shop_id,source,external_id,entity_type,entity_id,content_hash,imported_at) VALUES
('f1000000-0000-4000-8000-000000000028',@shop,N'fixture',N'row-1',N'customer',@customer,REPLICATE(N'f',64),'2035-09-02T12:00:24.123456');
`

const sqlServerCurrentLogicalFixture = `
INSERT INTO dbo.property_photos(id,shop_id,property_id,submission_key,content_hash,caption,category,content_type,image_data,recorded_by,recorded_at,deleted_at,object_key,object_version,object_pending) VALUES
('f1000000-0000-4000-8000-000000000033','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000011',N'66666666666666666666666666666666',N'039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81',N'Pending inline copy',N'general',N'image/png',0x010203,N'Owner','2035-09-02T12:00:24.123456',NULL,REPLICATE(N'a',64),NULL,1),
('f1000000-0000-4000-8000-000000000036','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000011',N'77777777777777777777777777777777',N'787c798e39a5bc1910355bae6d0cd87a36b2e10fd0202a83e3bb6b005da83472',N'Legacy backfill',N'general',N'image/png',0x040506,N'Owner','2035-09-02T12:00:24.123456',NULL,REPLICATE(N'e',64),NULL,0),
('f1000000-0000-4000-8000-000000000037','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000011',N'88888888888888888888888888888888',N'5a8814ae66ff07179d2c22381da6221f6fe754e6175c47d7d87846080f0a9715',N'',N'general',N'image/png',NULL,N'Owner','2035-09-02T12:00:24.123456','2035-09-03T12:00:24.123456',REPLICATE(N'f',64),N'version-linked-purged',0);
INSERT INTO dbo.property_photo_objects(object_key,shop_id,photo_id,content_hash,content_type,byte_length,version_id,created_at,retired_at,purged_at) VALUES
(REPLICATE(N'a',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000033',N'039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81',N'image/png',3,NULL,'2035-09-02T12:00:25.123456',NULL,NULL),
(REPLICATE(N'b',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000034',N'd798d1fac6bd4bb1c11f50312760351013379a0ab6f0a8c0af8a506b96b2525a',N'image/jpeg',2,N'version-retired','2035-09-02T12:00:25.123456','2035-09-03T12:00:25.123456',NULL),
(REPLICATE(N'c',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000035',N'5a8814ae66ff07179d2c22381da6221f6fe754e6175c47d7d87846080f0a9715',N'image/png',2,N'version-purged','2035-09-02T12:00:25.123456','2035-09-03T12:00:25.123456','2035-09-10T12:00:25.123456'),
(REPLICATE(N'd',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000016',N'5240672d7b51756b829ad0ef8d9468b7a078afa2f410484fd3892dab47becb72',N'image/png',3,N'version-1','2035-09-02T12:00:25.123456',NULL,NULL),
(REPLICATE(N'e',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000036',N'787c798e39a5bc1910355bae6d0cd87a36b2e10fd0202a83e3bb6b005da83472',N'image/png',3,NULL,'2035-09-02T12:00:25.123456',NULL,NULL),
(REPLICATE(N'f',64),'f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000037',N'5a8814ae66ff07179d2c22381da6221f6fe754e6175c47d7d87846080f0a9715',N'image/png',2,N'version-linked-purged','2035-09-02T12:00:25.123456','2035-09-03T12:00:25.123456','2035-09-10T12:00:25.123456');
INSERT INTO dbo.jobs(id,shop_id,customer_id,property_id,submission_key,creation_fingerprint,kind,title,details,scheduled_on,status,completed_at,revision,created_at,updated_at) VALUES
('f1000000-0000-4000-8000-000000000030','f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000010','f1000000-0000-4000-8000-000000000011',N'55555555555555555555555555555555',REPLICATE(N'e',64),N'survey',N'Site survey',N'{"zones":["north","南"],"empty":""}', '2035-09-03',N'planned',NULL,1,'2035-09-02T12:00:26.123456','2035-09-02T12:00:27.123456');
INSERT INTO dbo.job_events(shop_id,job_id,revision,action,snapshot,recorded_by,recorded_at) VALUES
('f1000000-0000-4000-8000-000000000001','f1000000-0000-4000-8000-000000000030',1,N'created',N'{"empty":"","tags":["alpha","β"]}',N'Owner','2035-09-02T12:00:28.123456');
`
