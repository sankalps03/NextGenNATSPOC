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
    tenant VARCHAR(255) NOT NULL,
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
COMMENT ON COLUMN tickets.companyid IS 'Company/tenant ID for multi-tenant isolation';
COMMENT ON COLUMN tickets.requesterid IS 'ID of user who requested the ticket';
COMMENT ON COLUMN tickets.technicianid IS 'ID of technician assigned to the ticket';
COMMENT ON COLUMN tickets.statusid IS 'Current status of the ticket';
COMMENT ON COLUMN tickets.priorityid IS 'Priority level of the ticket';
COMMENT ON COLUMN tickets.dueby IS 'Unix timestamp when ticket is due for resolution';

-- Indexes for specified columns only (excluding primary key 'id')
-- User and assignment fields
CREATE INDEX idx_tickets_updatedbyid ON tickets(updatedbyid);
CREATE INDEX idx_tickets_createdbyid ON tickets(createdbyid);
CREATE INDEX idx_tickets_removedbyid ON tickets(removedbyid);
CREATE INDEX idx_tickets_requesterid ON tickets(requesterid);
CREATE INDEX idx_tickets_technicianid ON tickets(technicianid);
CREATE INDEX idx_tickets_closedby ON tickets(closedby);
CREATE INDEX idx_tickets_resolvedby ON tickets(resolvedby);

-- Organizational and categorization fields
CREATE INDEX idx_tickets_departmentid ON tickets(departmentid);
CREATE INDEX idx_tickets_groupid ON tickets(groupid);
CREATE INDEX idx_tickets_impactid ON tickets(impactid);
CREATE INDEX idx_tickets_locationid ON tickets(locationid);
CREATE INDEX idx_tickets_priorityid ON tickets(priorityid);
CREATE INDEX idx_tickets_resolutionduelevel ON tickets(resolutionduelevel);
CREATE INDEX idx_tickets_responseduelevel ON tickets(responseduelevel);
CREATE INDEX idx_tickets_statusid ON tickets(statusid);
CREATE INDEX idx_tickets_templateid ON tickets(templateid);
CREATE INDEX idx_tickets_urgencyid ON tickets(urgencyid);
CREATE INDEX idx_tickets_violatedslaid ON tickets(violatedslaid);
CREATE INDEX idx_tickets_emailreadconfigid ON tickets(emailreadconfigid);
CREATE INDEX idx_tickets_requesttype ON tickets(requesttype);
CREATE INDEX idx_tickets_servicecatalogid ON tickets(servicecatalogid);
CREATE INDEX idx_tickets_sourceid ON tickets(sourceid);
CREATE INDEX idx_tickets_oladuelevel ON tickets(oladuelevel);
CREATE INDEX idx_tickets_suggestedcategoryid ON tickets(suggestedcategoryid);
CREATE INDEX idx_tickets_suggestedgroupid ON tickets(suggestedgroupid);
CREATE INDEX idx_tickets_companyid ON tickets(companyid);
CREATE INDEX idx_tickets_vendorid ON tickets(vendorid);
CREATE INDEX idx_tickets_violateducid ON tickets(violateducid);
CREATE INDEX idx_tickets_transitionmodelid ON tickets(transitionmodelid);
CREATE INDEX idx_tickets_messengerconfigid ON tickets(messengerconfigid);

-- Timestamp fields
CREATE INDEX idx_tickets_updatedtime ON tickets(updatedtime);
CREATE INDEX idx_tickets_createdtime ON tickets(createdtime);
CREATE INDEX idx_tickets_removedtime ON tickets(removedtime);
CREATE INDEX idx_tickets_lastclosedtime ON tickets(lastclosedtime);
CREATE INDEX idx_tickets_lastopenedtime ON tickets(lastopenedtime);
CREATE INDEX idx_tickets_lastresolvedtime ON tickets(lastresolvedtime);
CREATE INDEX idx_tickets_lastviolationtime ON tickets(lastviolationtime);
CREATE INDEX idx_tickets_olddueby ON tickets(olddueby);
CREATE INDEX idx_tickets_oldresponsedue ON tickets(oldresponsedue);
CREATE INDEX idx_tickets_responsedue ON tickets(responsedue);
CREATE INDEX idx_tickets_responseescalationtime ON tickets(responseescalationtime);
CREATE INDEX idx_tickets_statuschangedtime ON tickets(statuschangedtime);
CREATE INDEX idx_tickets_groupchangedtime ON tickets(groupchangedtime);
CREATE INDEX idx_tickets_oladueby ON tickets(oladueby);
CREATE INDEX idx_tickets_olaescalationtime ON tickets(olaescalationtime);
CREATE INDEX idx_tickets_askfeedbackdate ON tickets(askfeedbackdate);
CREATE INDEX idx_tickets_firstfeedbackdate ON tickets(firstfeedbackdate);
CREATE INDEX idx_tickets_lastucviolationtime ON tickets(lastucviolationtime);
CREATE INDEX idx_tickets_lastapproveddate ON tickets(lastapproveddate);

-- Composite indexes for common query patterns
CREATE INDEX idx_tickets_tenant_status ON tickets(tenant, statusid);
CREATE INDEX idx_tickets_company_status ON tickets(companyid, statusid);
CREATE INDEX idx_tickets_requester_status ON tickets(requesterid, statusid);
CREATE INDEX idx_tickets_technician_status ON tickets(technicianid, statusid);
CREATE INDEX idx_tickets_group_priority ON tickets(groupid, priorityid);
CREATE INDEX idx_tickets_created_status ON tickets(createdtime, statusid);