-- Migration utilities for converting from DynamoDB to PostgreSQL
-- Ticket Management System

-- Create temporary staging table for CSV import
CREATE TABLE IF NOT EXISTS tickets_staging (
    id_original BIGINT,
    updatedbyid BIGINT,
    updatedtime BIGINT,
    createdbyid BIGINT,
    createdtime BIGINT,
    name VARCHAR(255),
    oobtype VARCHAR(100),
    removed_text VARCHAR(10),
    removedbyid BIGINT,
    removedtime BIGINT,
    approvalstatus INTEGER,
    approvaltype INTEGER,
    categoryid BIGINT,
    departmentid BIGINT,
    description TEXT,
    dueby BIGINT,
    duetimemanuallyupdated_text VARCHAR(10),
    firstresponsetime BIGINT,
    groupid BIGINT,
    impactid BIGINT,
    lastclosedtime BIGINT,
    lastopenedtime BIGINT,
    lastresolvedtime BIGINT,
    lastviolationtime BIGINT,
    locationid BIGINT,
    olddueby BIGINT,
    oldresponsedue BIGINT,
    originaldescription TEXT,
    priorityid BIGINT,
    reopened_text VARCHAR(10),
    requesterid BIGINT,
    resolutionduelevel INTEGER,
    resolutionescalationtime BIGINT,
    responsedue BIGINT,
    responseduelevel INTEGER,
    responsedueviolated_text VARCHAR(10),
    responseescalationtime BIGINT,
    slaviolated_text VARCHAR(10),
    statuschangedtime BIGINT,
    statusid BIGINT,
    subject VARCHAR(500),
    supportlevel INTEGER,
    technicianid BIGINT,
    templateid BIGINT,
    totalonholdduration BIGINT,
    totalresolutiontime BIGINT,
    totalslapausetime BIGINT,
    totalworkingtime BIGINT,
    urgencyid BIGINT,
    violatedslaid BIGINT,
    callfrom VARCHAR(100),
    emailreadconfigemail VARCHAR(255),
    emailreadconfigid BIGINT,
    purchaserequest_text VARCHAR(10),
    requesttype VARCHAR(100),
    servicecatalogid BIGINT,
    sourceid BIGINT,
    spam_text VARCHAR(10),
    viprequest_text VARCHAR(10),
    groupchangedtime BIGINT,
    lastolaviolationtime BIGINT,
    oladueby BIGINT,
    olaviolated_text VARCHAR(10),
    oldoladueby BIGINT,
    askfeedbackdate BIGINT,
    firstfeedbackdate BIGINT,
    oladuelevel INTEGER,
    olaescalationtime BIGINT,
    suggestedcategoryid BIGINT,
    suggestedgroupid BIGINT,
    companyid BIGINT,
    closedby BIGINT,
    resolvedby BIGINT,
    vendorid BIGINT,
    lastucviolationtime BIGINT,
    olducdueby BIGINT,
    totaluconholdduration BIGINT,
    totalucpausetime BIGINT,
    totalucworkingtime BIGINT,
    ucdueby BIGINT,
    ucduelevel INTEGER,
    ucescalationtime BIGINT,
    ucviolated_text VARCHAR(10),
    violateducid BIGINT,
    totalucresolutiontime BIGINT,
    transitionmodelid BIGINT,
    migrated_text VARCHAR(10),
    mergedrequest BIGINT,
    messengerconfigid BIGINT,
    lastapproveddate BIGINT
);

-- Function to convert text boolean values to actual boolean
CREATE OR REPLACE FUNCTION text_to_boolean(text_value TEXT)
RETURNS BOOLEAN AS $$
BEGIN
    CASE LOWER(TRIM(text_value))
        WHEN 'true', 't', '1', 'yes', 'y' THEN RETURN TRUE;
        WHEN 'false', 'f', '0', 'no', 'n', '' THEN RETURN FALSE;
        ELSE RETURN FALSE;
    END CASE;
END;
$$ LANGUAGE plpgsql;

-- Function to safely convert text to bigint
CREATE OR REPLACE FUNCTION safe_text_to_bigint(text_value TEXT)
RETURNS BIGINT AS $$
BEGIN
    IF text_value IS NULL OR TRIM(text_value) = '' THEN
        RETURN 0;
    END IF;
    
    BEGIN
        RETURN text_value::BIGINT;
    EXCEPTION WHEN OTHERS THEN
        RETURN 0;
    END;
END;
$$ LANGUAGE plpgsql;

-- Migration procedure to transfer data from staging to main table
CREATE OR REPLACE FUNCTION migrate_tickets_from_staging()
RETURNS INTEGER AS $$
DECLARE
    migrated_count INTEGER := 0;
    rec RECORD;
BEGIN
    -- Clear existing data if needed (uncomment if you want to start fresh)
    -- TRUNCATE TABLE tickets RESTART IDENTITY;
    
    FOR rec IN SELECT * FROM tickets_staging LOOP
        INSERT INTO tickets (
            updatedbyid, updatedtime, createdbyid, createdtime, name, oobtype, removed, removedbyid, removedtime,
            approvalstatus, approvaltype, categoryid, departmentid, description, dueby, duetimemanuallyupdated,
            firstresponsetime, groupid, impactid, lastclosedtime, lastopenedtime, lastresolvedtime, lastviolationtime,
            locationid, olddueby, oldresponsedue, originaldescription, priorityid, reopened, requesterid,
            resolutionduelevel, resolutionescalationtime, responsedue, responseduelevel, responsedueviolated,
            responseescalationtime, slaviolated, statuschangedtime, statusid, subject, supportlevel, technicianid,
            templateid, totalonholdduration, totalresolutiontime, totalslapausetime, totalworkingtime, urgencyid,
            violatedslaid, callfrom, emailreadconfigemail, emailreadconfigid, purchaserequest, requesttype,
            servicecatalogid, sourceid, spam, viprequest, groupchangedtime, lastolaviolationtime, oladueby,
            olaviolated, oldoladueby, askfeedbackdate, firstfeedbackdate, oladuelevel, olaescalationtime,
            suggestedcategoryid, suggestedgroupid, companyid, closedby, resolvedby, vendorid, lastucviolationtime,
            olducdueby, totaluconholdduration, totalucpausetime, totalucworkingtime, ucdueby, ucduelevel,
            ucescalationtime, ucviolated, violateducid, totalucresolutiontime, transitionmodelid, migrated,
            messengerconfigid, lastapproveddate
        ) VALUES (
            rec.updatedbyid, rec.updatedtime, rec.createdbyid, rec.createdtime, rec.name, rec.oobtype, 
            text_to_boolean(rec.removed_text), rec.removedbyid, rec.removedtime,
            rec.approvalstatus, rec.approvaltype, rec.categoryid, rec.departmentid, rec.description, 
            rec.dueby, text_to_boolean(rec.duetimemanuallyupdated_text),
            rec.firstresponsetime, rec.groupid, rec.impactid, rec.lastclosedtime, rec.lastopenedtime, 
            rec.lastresolvedtime, rec.lastviolationtime,
            rec.locationid, rec.olddueby, rec.oldresponsedue, rec.originaldescription, rec.priorityid, 
            text_to_boolean(rec.reopened_text), rec.requesterid,
            rec.resolutionduelevel, rec.resolutionescalationtime, rec.responsedue, rec.responseduelevel, 
            text_to_boolean(rec.responsedueviolated_text),
            rec.responseescalationtime, text_to_boolean(rec.slaviolated_text), rec.statuschangedtime, 
            rec.statusid, rec.subject, rec.supportlevel, rec.technicianid,
            rec.templateid, rec.totalonholdduration, rec.totalresolutiontime, rec.totalslapausetime, 
            rec.totalworkingtime, rec.urgencyid,
            rec.violatedslaid, rec.callfrom, rec.emailreadconfigemail, rec.emailreadconfigid, 
            text_to_boolean(rec.purchaserequest_text), rec.requesttype,
            rec.servicecatalogid, rec.sourceid, text_to_boolean(rec.spam_text), 
            text_to_boolean(rec.viprequest_text), rec.groupchangedtime, rec.lastolaviolationtime, rec.oladueby,
            text_to_boolean(rec.olaviolated_text), rec.oldoladueby, rec.askfeedbackdate, rec.firstfeedbackdate, 
            rec.oladuelevel, rec.olaescalationtime,
            rec.suggestedcategoryid, rec.suggestedgroupid, rec.companyid, rec.closedby, rec.resolvedby, 
            rec.vendorid, rec.lastucviolationtime,
            rec.olducdueby, rec.totaluconholdduration, rec.totalucpausetime, rec.totalucworkingtime, 
            rec.ucdueby, rec.ucduelevel,
            rec.ucescalationtime, text_to_boolean(rec.ucviolated_text), rec.violateducid, 
            rec.totalucresolutiontime, rec.transitionmodelid, text_to_boolean(rec.migrated_text),
            rec.messengerconfigid, rec.lastapproveddate
        );
        
        migrated_count := migrated_count + 1;
    END LOOP;
    
    RETURN migrated_count;
END;
$$ LANGUAGE plpgsql;

-- Data validation queries
CREATE OR REPLACE FUNCTION validate_migration()
RETURNS TABLE(
    validation_check TEXT,
    staging_count BIGINT,
    main_count BIGINT,
    status TEXT
) AS $$
BEGIN
    -- Total record count
    RETURN QUERY
    SELECT 
        'Total Records'::TEXT,
        (SELECT COUNT(*) FROM tickets_staging)::BIGINT,
        (SELECT COUNT(*) FROM tickets)::BIGINT,
        CASE 
            WHEN (SELECT COUNT(*) FROM tickets_staging) = (SELECT COUNT(*) FROM tickets) 
            THEN 'PASS'::TEXT 
            ELSE 'FAIL'::TEXT 
        END;
    
    -- Non-null requesterid count
    RETURN QUERY
    SELECT 
        'Non-null Requester IDs'::TEXT,
        (SELECT COUNT(*) FROM tickets_staging WHERE requesterid IS NOT NULL AND requesterid > 0)::BIGINT,
        (SELECT COUNT(*) FROM tickets WHERE requesterid IS NOT NULL AND requesterid > 0)::BIGINT,
        CASE 
            WHEN (SELECT COUNT(*) FROM tickets_staging WHERE requesterid IS NOT NULL AND requesterid > 0) = 
                 (SELECT COUNT(*) FROM tickets WHERE requesterid IS NOT NULL AND requesterid > 0)
            THEN 'PASS'::TEXT 
            ELSE 'FAIL'::TEXT 
        END;
    
    -- Boolean conversion check
    RETURN QUERY
    SELECT 
        'Boolean Fields Converted'::TEXT,
        (SELECT COUNT(*) FROM tickets_staging WHERE removed_text = 'true')::BIGINT,
        (SELECT COUNT(*) FROM tickets WHERE removed = true)::BIGINT,
        CASE 
            WHEN (SELECT COUNT(*) FROM tickets_staging WHERE removed_text = 'true') = 
                 (SELECT COUNT(*) FROM tickets WHERE removed = true)
            THEN 'PASS'::TEXT 
            ELSE 'FAIL'::TEXT 
        END;
END;
$$ LANGUAGE plpgsql;

-- Performance comparison queries
CREATE OR REPLACE FUNCTION performance_test_queries()
RETURNS TABLE(
    query_description TEXT,
    execution_time_ms NUMERIC
) AS $$
DECLARE
    start_time TIMESTAMP;
    end_time TIMESTAMP;
BEGIN
    -- Test 1: Simple ID lookup
    start_time := clock_timestamp();
    PERFORM * FROM tickets WHERE id = 1;
    end_time := clock_timestamp();
    
    RETURN QUERY
    SELECT 
        'Single ticket lookup by ID'::TEXT,
        EXTRACT(MILLISECONDS FROM (end_time - start_time))::NUMERIC;
    
    -- Test 2: Requester search
    start_time := clock_timestamp();
    PERFORM * FROM tickets WHERE requesterid = 7193 LIMIT 100;
    end_time := clock_timestamp();
    
    RETURN QUERY
    SELECT 
        'Tickets by requester (limit 100)'::TEXT,
        EXTRACT(MILLISECONDS FROM (end_time - start_time))::NUMERIC;
    
    -- Test 3: Status aggregation
    start_time := clock_timestamp();
    PERFORM statusid, COUNT(*) FROM tickets GROUP BY statusid;
    end_time := clock_timestamp();
    
    RETURN QUERY
    SELECT 
        'Status aggregation query'::TEXT,
        EXTRACT(MILLISECONDS FROM (end_time - start_time))::NUMERIC;
    
    -- Test 4: Time range query
    start_time := clock_timestamp();
    PERFORM * FROM tickets 
    WHERE createdtime BETWEEN 1648546130000 AND 1648632530000 
    LIMIT 100;
    end_time := clock_timestamp();
    
    RETURN QUERY
    SELECT 
        'Time range query (limit 100)'::TEXT,
        EXTRACT(MILLISECONDS FROM (end_time - start_time))::NUMERIC;
END;
$$ LANGUAGE plpgsql;

-- Usage instructions as comments:
/*
MIGRATION STEPS:

1. Load CSV data into staging table:
   \copy tickets_staging FROM '/path/to/your/tickets.csv' WITH CSV HEADER;

2. Run migration:
   SELECT migrate_tickets_from_staging();

3. Validate migration:
   SELECT * FROM validate_migration();

4. Test performance:
   SELECT * FROM performance_test_queries();

5. Clean up staging table:
   DROP TABLE tickets_staging;

6. Update sequence to match highest ID (if needed):
   SELECT setval('tickets_id_seq', (SELECT MAX(id) FROM tickets));

EXAMPLE CSV IMPORT:
If your CSV file is at /tmp/tickets.csv:
\copy tickets_staging FROM '/tmp/tickets.csv' WITH CSV HEADER DELIMITER ',';
*/
