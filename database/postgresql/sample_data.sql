-- Sample Data and Queries for PostgreSQL Ticket Management System
-- Based on the provided CSV data

-- Insert sample data based on your CSV row
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
    6971, 1648552922266, 7193, 1648546130945, 'SR-4866', '', false, 0, 0,
    0, 0, 0, 0, 'Extension', -1648552921995, false,
    1648552918160, 0, 1, 1648552921993, 1648546130941, 1648552921993, 0,
    0, 1648821600000, 1648632540000, '<p>Extension</p>', 1, false, 7193,
    0, 1648735200000, -1648552921995, 0, false,
    1648718940000, false, 1648552921992, 12, 'Article Extension', 0, 6971,
    0, 0, 6720000, 0, 6720000, 1,
    4, '', '', 0, false, '',
    231, 2, false, false, 0, 0, 0,
    false, 0, 1648552924388, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, false,
    0, 0
);

-- Sample queries for common operations

-- 1. Find tickets by requester
SELECT id, name, subject, statusid, priorityid, createdtime
FROM tickets 
WHERE requesterid = 7193
ORDER BY createdtime DESC;

-- 2. Find tickets by technician
SELECT id, name, subject, statusid, priorityid, createdtime
FROM tickets 
WHERE technicianid = 6971
ORDER BY createdtime DESC;

-- 3. Find tickets by status
SELECT id, name, subject, requesterid, technicianid, createdtime
FROM tickets 
WHERE statusid = 12
ORDER BY createdtime DESC;

-- 4. Find overdue tickets (dueby in the past, assuming current time > dueby)
SELECT id, name, subject, dueby, statusid, priorityid
FROM tickets 
WHERE dueby > 0 AND dueby < EXTRACT(EPOCH FROM NOW()) * 1000
ORDER BY dueby ASC;

-- 5. Find tickets created in the last 24 hours
SELECT id, name, subject, requesterid, statusid, createdtime
FROM tickets 
WHERE createdtime > EXTRACT(EPOCH FROM NOW() - INTERVAL '24 hours') * 1000
ORDER BY createdtime DESC;

-- 6. Find tickets by company (multi-tenant query)
SELECT id, name, subject, requesterid, statusid, createdtime
FROM tickets 
WHERE companyid = 0
ORDER BY createdtime DESC;

-- 7. Find tickets with SLA violations
SELECT id, name, subject, lastviolationtime, violatedslaid, statusid
FROM tickets 
WHERE lastviolationtime > 0
ORDER BY lastviolationtime DESC;

-- 8. Performance analytics - tickets by status count
SELECT statusid, COUNT(*) as ticket_count
FROM tickets 
GROUP BY statusid
ORDER BY ticket_count DESC;

-- 9. Performance analytics - tickets by priority
SELECT priorityid, COUNT(*) as ticket_count
FROM tickets 
GROUP BY priorityid
ORDER BY priorityid;

-- 10. Performance analytics - average resolution time by priority
SELECT priorityid, 
       AVG(totalresolutiontime) as avg_resolution_time_ms,
       AVG(totalresolutiontime / 1000.0 / 60.0) as avg_resolution_time_minutes
FROM tickets 
WHERE totalresolutiontime > 0
GROUP BY priorityid
ORDER BY priorityid;

-- 11. Find tickets assigned to specific group
SELECT id, name, subject, requesterid, technicianid, statusid
FROM tickets 
WHERE groupid = 0
ORDER BY createdtime DESC;

-- 12. Find tickets by category
SELECT id, name, subject, categoryid, statusid, createdtime
FROM tickets 
WHERE categoryid = 0
ORDER BY createdtime DESC;

-- 13. Complex query - Open tickets by technician with priority
SELECT t.id, t.name, t.subject, t.priorityid, t.createdtime, t.dueby
FROM tickets t
WHERE t.technicianid = 6971 
  AND t.statusid NOT IN (12) -- Assuming 12 is closed status
  AND t.removed = false
ORDER BY t.priorityid DESC, t.createdtime ASC;

-- 14. Time-based analytics - tickets created per day
SELECT DATE(TO_TIMESTAMP(createdtime / 1000)) as creation_date,
       COUNT(*) as tickets_created
FROM tickets 
WHERE createdtime > 0
GROUP BY DATE(TO_TIMESTAMP(createdtime / 1000))
ORDER BY creation_date DESC;

-- 15. SLA performance - tickets resolved within SLA
SELECT 
    COUNT(*) as total_tickets,
    COUNT(CASE WHEN lastresolvedtime <= dueby AND dueby > 0 THEN 1 END) as resolved_within_sla,
    ROUND(
        (COUNT(CASE WHEN lastresolvedtime <= dueby AND dueby > 0 THEN 1 END) * 100.0 / 
         NULLIF(COUNT(CASE WHEN dueby > 0 THEN 1 END), 0)), 2
    ) as sla_compliance_percentage
FROM tickets 
WHERE lastresolvedtime > 0;

-- Utility queries for database maintenance

-- Check table size and row count
SELECT 
    schemaname,
    tablename,
    attname,
    n_distinct,
    correlation
FROM pg_stats 
WHERE tablename = 'tickets';

-- Check index usage
SELECT 
    indexrelname as index_name,
    idx_tup_read,
    idx_tup_fetch,
    idx_scan
FROM pg_stat_user_indexes 
WHERE relname = 'tickets'
ORDER BY idx_scan DESC;

-- Analyze table for query optimization
ANALYZE tickets;

-- Example of adding a new ticket (with auto-increment ID)
INSERT INTO tickets (
    createdbyid, requesterid, name, subject, description, 
    statusid, priorityid, urgencyid, categoryid, companyid,
    createdtime, updatedtime
) VALUES (
    1001, 1001, 'SR-NEW', 'New Test Ticket', 'This is a test ticket',
    1, 2, 1, 5, 1,
    EXTRACT(EPOCH FROM NOW()) * 1000,
    EXTRACT(EPOCH FROM NOW()) * 1000
);

-- Example of updating a ticket
UPDATE tickets 
SET statusid = 2, 
    technicianid = 6971,
    updatedtime = EXTRACT(EPOCH FROM NOW()) * 1000
WHERE id = 1;

-- Example of soft delete (marking as removed instead of actual deletion)
UPDATE tickets 
SET removed = true, 
    removedtime = EXTRACT(EPOCH FROM NOW()) * 1000,
    removedbyid = 1001
WHERE id = 1;
