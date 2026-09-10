package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

// The authored fixture is small and fixed. One payload keeps native seeding from
// requiring a round trip per record while all six inserts share the supplied tx.
func insertSQLServerPublicDemoFixture(ctx context.Context, tx database.Tx, shop string, now time.Time) error {
	now = now.UTC().Truncate(time.Microsecond)
	payload := struct {
		Plans      []publicDemoPlanFixture
		Customers  []publicDemoCustomerFixture
		Properties []publicDemoPropertyFixture
		Equipment  []map[string]any
		Agreements []map[string]any
		Schedules  []map[string]any
	}{Plans: publicDemoFixture.Plans, Customers: publicDemoFixture.Customers, Properties: publicDemoFixture.Properties}
	day := func(t time.Time) (string, error) {
		if t.UTC().Year() < 1 || t.UTC().Year() > 9999 {
			return "", fmt.Errorf("public demo date outside supported range")
		}
		return t.UTC().Format("2006-01-02"), nil
	}
	for _, v := range publicDemoFixture.Equipment {
		installed, err := day(now.AddDate(0, -v.InstalledMonthsAgo, 0))
		if err != nil {
			return err
		}
		payload.Equipment = append(payload.Equipment, map[string]any{"Key": v.Key, "PropertyKey": v.PropertyKey, "Kind": v.Kind, "Make": v.Make, "Model": v.Model, "Identifier": v.Identifier, "InstalledOn": installed})
	}
	for _, v := range publicDemoFixture.Agreements {
		date, err := day(now.AddDate(0, 0, v.RenewalDays))
		if err != nil {
			return err
		}
		payload.Agreements = append(payload.Agreements, map[string]any{"Key": v.Key, "CustomerKey": v.CustomerKey, "PropertyKey": v.PropertyKey, "PlanKey": v.PlanKey, "Status": string(v.Status), "AnniversaryDate": date, "VisitsEntitled": v.VisitsEntitled, "VisitsUsed": v.VisitsUsed, "PriceCents": v.PriceCents, "Notes": v.Notes})
	}
	for _, v := range publicDemoFixture.Schedules {
		start, err := day(now.AddDate(0, 0, -v.StartsDaysAgo))
		if err != nil {
			return err
		}
		var end, paused any
		if v.HasEnd {
			end, err = day(now.AddDate(0, 0, v.EndsDaysFromNow))
			if err != nil {
				return err
			}
		}
		if !v.Active {
			at := now.AddDate(0, 0, -7)
			if _, err := day(at); err != nil {
				return err
			}
			paused = at.Format(time.RFC3339Nano)
		}
		payload.Schedules = append(payload.Schedules, map[string]any{"Key": v.Key, "AgreementKey": v.AgreementKey, "ServiceName": v.ServiceName, "CadenceUnit": v.CadenceUnit, "CadenceInterval": v.CadenceInterval, "StartsOn": start, "EndsOn": end, "Active": v.Active, "PausedAt": paused})
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, sqlServerPublicFixture, shop, string(raw), now, publicDemoPlanCount, publicDemoCustomerCount, publicDemoPropertyCount, publicDemoEquipmentCount, publicDemoAgreementCount, publicDemoScheduleCount)
	return err
}

const sqlServerPublicFixture = `
DECLARE @shop uniqueidentifier=CONVERT(uniqueidentifier,@p1);
DECLARE @plans TABLE([key] nvarchar(100) COLLATE Latin1_General_100_BIN2 PRIMARY KEY,id uniqueidentifier NOT NULL);
DECLARE @customers TABLE([key] nvarchar(100) COLLATE Latin1_General_100_BIN2 PRIMARY KEY,id uniqueidentifier NOT NULL);
DECLARE @properties TABLE([key] nvarchar(100) COLLATE Latin1_General_100_BIN2 PRIMARY KEY,id uniqueidentifier NOT NULL);
DECLARE @agreements TABLE([key] nvarchar(100) COLLATE Latin1_General_100_BIN2 PRIMARY KEY,id uniqueidentifier NOT NULL);
INSERT INTO @plans SELECT [Key],NEWID() FROM OPENJSON(@p2,'$.Plans') WITH([Key] nvarchar(100));
INSERT INTO @customers SELECT [Key],NEWID() FROM OPENJSON(@p2,'$.Customers') WITH([Key] nvarchar(100));
INSERT INTO @properties SELECT [Key],NEWID() FROM OPENJSON(@p2,'$.Properties') WITH([Key] nvarchar(100));
INSERT INTO @agreements SELECT [Key],NEWID() FROM OPENJSON(@p2,'$.Agreements') WITH([Key] nvarchar(100));
INSERT INTO dbo.plan_templates(id,shop_id,name,description,price_cents,billing_interval,term_months,visits_per_year,visit_windows)
 SELECT m.id,@shop,j.Name,j.Description,j.PriceCents,j.BillingInterval,j.TermMonths,j.VisitsPerYear,j.VisitWindows FROM OPENJSON(@p2,'$.Plans') WITH([Key] nvarchar(100),Name nvarchar(max),Description nvarchar(max),PriceCents int,BillingInterval nvarchar(20),TermMonths int,VisitsPerYear int,VisitWindows nvarchar(max) AS JSON) j JOIN @plans m ON m.[key]=j.[Key];
IF @@ROWCOUNT<>@p4 THROW 51000,'public demo plan mapping incomplete',1;
INSERT INTO dbo.customers(id,shop_id,external_id,name,email,phone)
 SELECT m.id,@shop,N'demo-customer-'+j.[Key],j.Name,CASE WHEN DATALENGTH(j.Email)=0 THEN NULL ELSE j.Email END,CASE WHEN DATALENGTH(j.Phone)=0 THEN NULL ELSE j.Phone END FROM OPENJSON(@p2,'$.Customers') WITH([Key] nvarchar(100),Name nvarchar(max),Email nvarchar(max),Phone nvarchar(max)) j JOIN @customers m ON m.[key]=j.[Key];
IF @@ROWCOUNT<>@p5 THROW 51000,'public demo customer mapping incomplete',1;
INSERT INTO dbo.properties(id,shop_id,customer_id,external_id,label,address_line1,address_line2,city,state,postal_code)
 SELECT m.id,@shop,c.id,N'demo-property-'+j.[Key],j.Label,j.AddressLine1,CASE WHEN DATALENGTH(j.AddressLine2)=0 THEN NULL ELSE j.AddressLine2 END,j.City,j.State,j.ZIP FROM OPENJSON(@p2,'$.Properties') WITH([Key] nvarchar(100),CustomerKey nvarchar(100),Label nvarchar(max),AddressLine1 nvarchar(max),AddressLine2 nvarchar(max),City nvarchar(max),State nvarchar(max),ZIP nvarchar(max)) j JOIN @properties m ON m.[key]=j.[Key] JOIN @customers c ON c.[key]=j.CustomerKey;
IF @@ROWCOUNT<>@p6 THROW 51000,'public demo property mapping incomplete',1;
INSERT INTO dbo.equipment(shop_id,property_id,external_id,kind,make,model,serial_number,installed_on)
 SELECT @shop,p.id,N'demo-equipment-'+j.[Key],j.Kind,CASE WHEN DATALENGTH(j.Make)=0 THEN NULL ELSE j.Make END,CASE WHEN DATALENGTH(j.Model)=0 THEN NULL ELSE j.Model END,j.Identifier,j.InstalledOn FROM OPENJSON(@p2,'$.Equipment') WITH([Key] nvarchar(100),PropertyKey nvarchar(100),Kind nvarchar(max),Make nvarchar(max),Model nvarchar(max),Identifier nvarchar(max),InstalledOn date) j JOIN @properties p ON p.[key]=j.PropertyKey;
IF @@ROWCOUNT<>@p7 THROW 51000,'public demo equipment mapping incomplete',1;
INSERT INTO dbo.agreements(id,shop_id,customer_id,property_id,plan_template_id,external_id,status,anniversary_date,visits_entitled,visits_used,price_cents,notes,plan_snapshot_name,plan_snapshot_description,plan_snapshot_billing_interval,plan_snapshot_term_months,plan_snapshot_visits_per_year,plan_snapshot_visit_windows,created_at,updated_at)
 SELECT a.id,@shop,c.id,p.id,pt.id,N'demo-agreement-'+j.[Key],j.Status,j.AnniversaryDate,j.VisitsEntitled,j.VisitsUsed,j.PriceCents,j.Notes,pt.name,COALESCE(pt.description,N''),pt.billing_interval,pt.term_months,pt.visits_per_year,pt.visit_windows,@p3,@p3
 FROM OPENJSON(@p2,'$.Agreements') WITH([Key] nvarchar(100),CustomerKey nvarchar(100),PropertyKey nvarchar(100),PlanKey nvarchar(100),Status nvarchar(30),AnniversaryDate date,VisitsEntitled int,VisitsUsed int,PriceCents int,Notes nvarchar(max)) j
 JOIN @agreements a ON a.[key]=j.[Key] JOIN @customers c ON c.[key]=j.CustomerKey JOIN @properties p ON p.[key]=j.PropertyKey JOIN @plans pm ON pm.[key]=j.PlanKey JOIN dbo.plan_templates pt ON pt.id=pm.id AND pt.shop_id=@shop;
IF @@ROWCOUNT<>@p8 THROW 51000,'public demo agreement mapping incomplete',1;
INSERT INTO dbo.service_schedules(shop_id,agreement_id,external_id,service_name,cadence_unit,cadence_interval,starts_on,ends_on,active,paused_at)
 SELECT @shop,a.id,N'demo-service-'+j.[Key],j.ServiceName,j.CadenceUnit,j.CadenceInterval,j.StartsOn,j.EndsOn,j.Active,j.PausedAt FROM OPENJSON(@p2,'$.Schedules') WITH([Key] nvarchar(100),AgreementKey nvarchar(100),ServiceName nvarchar(max),CadenceUnit nvarchar(10),CadenceInterval int,StartsOn date,EndsOn date,Active bit,PausedAt datetime2(6)) j JOIN @agreements a ON a.[key]=j.AgreementKey;
IF @@ROWCOUNT<>@p9 THROW 51000,'public demo schedule mapping incomplete',1;`
