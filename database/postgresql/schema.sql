-- PostgreSQL Schema for Ticket Management System
-- Based on CSV data structure with auto-increment primary key
-- Created: 2025-09-08

-- Drop table if exists (for clean recreation)
DROP TABLE IF EXISTS tickets CASCADE;

-- Create tickets table with all fields from CSV data
CREATE TABLE tickets (
    -- Primary key with auto-increment
    id BIGSERIAL PRIMARY KEY,

    -- Core fields for protobuf compatibility (managed by application)
    ticket_id VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    -- User and assignment fields
    updatedbyid BIGINT,
    createdbyid BIGINT,
    removedbyid BIGINT,
    requesterid BIGINT,
    technicianid BIGINT,
    closedby BIGINT,
    resolvedby BIGINT,
    
    -- Timestamp fields (Unix timestamps in milliseconds)
    updatedtime BIGINT,
    createdtime BIGINT,
    removedtime BIGINT,
    dueby BIGINT,
    firstresponsetime BIGINT,
    lastclosedtime BIGINT,
    lastopenedtime BIGINT,
    lastresolvedtime BIGINT,
    lastviolationtime BIGINT,
    olddueby BIGINT,
    oldresponsedue BIGINT,
    resolutionescalationtime BIGINT,
    responsedue BIGINT,
    responseescalationtime BIGINT,
    statuschangedtime BIGINT,
    groupchangedtime BIGINT,
    lastolaviolationtime BIGINT,
    oladueby BIGINT,
    oldoladueby BIGINT,
    askfeedbackdate BIGINT,
    firstfeedbackdate BIGINT,
    olaescalationtime BIGINT,
    lastucviolationtime BIGINT,
    olducdueby BIGINT,
    ucdueby BIGINT,
    ucescalationtime BIGINT,
    lastapproveddate BIGINT,
    
    -- Text fields
    name VARCHAR(255),
    oobtype VARCHAR(100),
    description TEXT,
    originaldescription TEXT,
    subject VARCHAR(500),
    callfrom VARCHAR(100),
    emailreadconfigemail VARCHAR(255),
    
    -- Boolean fields
    removed BOOLEAN DEFAULT FALSE,
    duetimemanuallyupdated BOOLEAN DEFAULT FALSE,
    reopened BOOLEAN DEFAULT FALSE,
    responsedueviolated BOOLEAN DEFAULT FALSE,
    slaviolated BOOLEAN DEFAULT FALSE,
    purchaserequest BOOLEAN DEFAULT FALSE,
    spam BOOLEAN DEFAULT FALSE,
    viprequest BOOLEAN DEFAULT FALSE,
    olaviolated BOOLEAN DEFAULT FALSE,
    ucviolated BOOLEAN DEFAULT FALSE,
    migrated BOOLEAN DEFAULT FALSE,
    
    -- Category and classification fields
    categoryid BIGINT,
    departmentid BIGINT,
    groupid BIGINT,
    impactid BIGINT,
    locationid BIGINT,
    priorityid BIGINT,
    statusid BIGINT,
    urgencyid BIGINT,
    violatedslaid BIGINT,
    servicecatalogid BIGINT,
    sourceid BIGINT,
    requesttype BIGINT,
    suggestedcategoryid BIGINT,
    suggestedgroupid BIGINT,
    companyid BIGINT,
    vendorid BIGINT,
    violateducid BIGINT,
    transitionmodelid BIGINT,
    messengerconfigid BIGINT,
    
    -- Approval and workflow fields
    approvalstatus INTEGER,
    approvaltype INTEGER,
    resolutionduelevel INTEGER,
    responseduelevel INTEGER,
    supportlevel INTEGER,
    oladuelevel INTEGER,
    ucduelevel INTEGER,
    
    -- Duration and time tracking fields (in milliseconds)
    totalonholdduration BIGINT DEFAULT 0,
    totalresolutiontime BIGINT DEFAULT 0,
    totalslapausetime BIGINT DEFAULT 0,
    totalworkingtime BIGINT DEFAULT 0,
    totaluconholdduration BIGINT DEFAULT 0,
    totalucpausetime BIGINT DEFAULT 0,
    totalucworkingtime BIGINT DEFAULT 0,
    totalucresolutiontime BIGINT DEFAULT 0,
    
    -- Configuration and template fields
    templateid BIGINT,
    emailreadconfigid BIGINT
);

-- Function to automatically update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Trigger to automatically update updated_at on row updates
CREATE TRIGGER update_tickets_updated_at 
    BEFORE UPDATE ON tickets 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

-- Comments for documentation
COMMENT ON TABLE tickets IS 'Main tickets table for ticket management system with auto-increment primary key';
COMMENT ON COLUMN tickets.id IS 'Auto-increment primary key';
COMMENT ON COLUMN tickets.createdtime IS 'Unix timestamp in milliseconds when ticket was created';
COMMENT ON COLUMN tickets.updatedtime IS 'Unix timestamp in milliseconds when ticket was last updated';
COMMENT ON COLUMN tickets.companyid IS 'Company ID for organizational grouping';
COMMENT ON COLUMN tickets.requesterid IS 'ID of user who requested the ticket';
COMMENT ON COLUMN tickets.technicianid IS 'ID of technician assigned to the ticket';
COMMENT ON COLUMN tickets.statusid IS 'Current status of the ticket';
COMMENT ON COLUMN tickets.priorityid IS 'Priority level of the ticket';
COMMENT ON COLUMN tickets.dueby IS 'Unix timestamp when ticket is due for resolution';

-- CLUSTERED INDEXES - Grouped by Business Domain
-- Replaces individual field indexes to reduce write burden and improve query performance

-- Essential individual indexes for primary lookups
CREATE UNIQUE INDEX idx_tickets_ticket_id ON tickets(ticket_id);
CREATE INDEX idx_tickets_createdtime ON tickets(createdtime);

-- 1. Request Metadata & Identity Cluster
-- Groups: createdbyid, requesterid, technicianid, groupid, departmentid
CREATE INDEX idx_tickets_request_identity ON tickets( requesterid, technicianid, groupid, departmentid, createdbyid);

-- 2. SLA & Response Tracking Cluster
-- Groups: dueby, firstresponsetime, responsedue, resolutionescalationtime, slaviolated
CREATE INDEX idx_tickets_sla_tracking ON tickets( dueby, firstresponsetime, responsedue, resolutionescalationtime, lastviolationtime);

-- 3. Status & Lifecycle Cluster
-- Groups: statusid, statuschangedtime, lastopenedtime, lastresolvedtime, lastclosedtime
CREATE INDEX idx_tickets_status_lifecycle ON tickets( statusid, statuschangedtime, lastopenedtime, lastresolvedtime, lastclosedtime);

-- 4. Priority, Urgency & Impact Cluster
-- Groups: priorityid, urgencyid, impactid, supportlevel, approvalstatus
CREATE INDEX idx_tickets_priority_impact ON tickets( priorityid, urgencyid, impactid, supportlevel, approvalstatus);

-- 5. OLA (Operational Level Agreements) Cluster
-- Groups: oladueby, oladuelevel, olaescalationtime, olaviolated, lastolaviolationtime
CREATE INDEX idx_tickets_ola_tracking ON tickets( oladueby, oladuelevel, olaescalationtime, lastolaviolationtime);

-- 6. UC (Underlying Contract) Cluster
-- Groups: ucdueby, ucduelevel, ucescalationtime, ucviolated, lastucviolationtime
CREATE INDEX idx_tickets_uc_tracking ON tickets( ucdueby, ucduelevel, ucescalationtime, lastucviolationtime);

-- 7. Timing & Durations Cluster
-- Groups: totalonholdduration, totalresolutiontime, totalslapausetime, totalworkingtime, reopened
CREATE INDEX idx_tickets_timing_durations ON tickets( totalonholdduration, totalresolutiontime, totalslapausetime, totalworkingtime, reopened);

-- 8. Feedback & Closure Cluster
-- Groups: askfeedbackdate, firstfeedbackdate, closedby, resolvedby, lastapproveddate
CREATE INDEX idx_tickets_feedback_closure ON tickets( closedby, resolvedby, askfeedbackdate, firstfeedbackdate, lastapproveddate);

-- 9. Category & Templates Cluster
-- Groups: categoryid, suggestedcategoryid, templateid, servicecatalogid, requesttype
CREATE INDEX idx_tickets_category_templates ON tickets( categoryid, templateid, servicecatalogid, requesttype, suggestedcategoryid);

-- 10. Misc/Integration Cluster
-- Groups: emailreadconfigid, messengerconfigid, vendorid, companyid
CREATE INDEX idx_tickets_integration_misc ON tickets( companyid, vendorid, emailreadconfigid, messengerconfigid);

-- Additional high-performance composite indexes for common query patterns
CREATE INDEX idx_tickets_requester_status_priority ON tickets(requesterid, statusid, priorityid);
CREATE INDEX idx_tickets_technician_status_created ON tickets(technicianid, statusid, createdtime);
CREATE INDEX idx_tickets_group_status_due ON tickets(groupid, statusid, dueby);
CREATE INDEX idx_tickets_company_category_status ON tickets(companyid, categoryid, statusid);
CREATE INDEX idx_tickets_created_status_priority ON tickets(createdtime, statusid, priorityid);