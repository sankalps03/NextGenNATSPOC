-- PostgreSQL Hybrid Schema for Ticket Management System
-- All fixed fields as columns + one custom JSONB field for dynamic data
-- Created: 2025-09-15

-- Grant necessary permissions to current user/role
GRANT USAGE ON SCHEMA public TO PUBLIC;
GRANT CREATE ON SCHEMA public TO PUBLIC;

-- Create uuid extension for generating unique IDs
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

GRANT ALL ON SCHEMA public TO PUBLIC;

ALTER DATABASE postgres SET search_path TO public, documentdb_core, documentdb_api, documentdb_api_catalog, documentdb_api_internal;

-- Drop table if exists (for clean recreation)
DROP TABLE IF EXISTS tickets CASCADE;

-- Create tickets table with all fixed fields + one custom JSONB field
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
                         emailreadconfigid BIGINT,

    -- Single custom field for dynamic data
                         custom_data documentdb_core.bson DEFAULT '{}'

    -- Constraint for ticket ID format
    );

-- Function to automatically update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
    RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    NEW.updatedtime = EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) * 1000; -- Unix timestamp in milliseconds
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Trigger to automatically update updated_at on row updates
CREATE TRIGGER update_tickets_updated_at
    BEFORE UPDATE ON tickets
    FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

-- INDEXES for Fixed Schema Fields
-- Essential individual indexes for primary lookups
CREATE UNIQUE INDEX idx_tickets_ticket_id ON tickets(ticket_id);
CREATE INDEX idx_tickets_created_at ON tickets(created_at);
CREATE INDEX idx_tickets_createdtime ON tickets(createdtime);

-- Fixed schema indexes (high-frequency query fields)
CREATE INDEX idx_tickets_requester_status ON tickets(requesterid, statusid);
CREATE INDEX idx_tickets_technician_status ON tickets(technicianid, statusid);
CREATE INDEX idx_tickets_priority_status ON tickets(priorityid, statusid);
CREATE INDEX idx_tickets_company_category ON tickets(companyid, categoryid);
CREATE INDEX idx_tickets_group_department ON tickets(groupid, departmentid);
CREATE INDEX idx_tickets_due_status ON tickets(dueby, statusid) WHERE dueby IS NOT NULL;

-- Specialized indexes for common field patterns
CREATE INDEX idx_tickets_status_created ON tickets(statusid, createdtime);
CREATE INDEX idx_tickets_requester_created ON tickets(requesterid, createdtime);
CREATE INDEX idx_tickets_company_status_priority ON tickets(companyid, statusid, priorityid);
CREATE INDEX idx_tickets_not_spam_not_removed ON tickets(statusid, priorityid) WHERE NOT spam AND NOT removed;

-- Text search index for subject and description
CREATE INDEX idx_tickets_text_search ON tickets USING GIN (to_tsvector('english', COALESCE(subject, '') || ' ' || COALESCE(description, '')));

-- Comments for documentation
COMMENT ON TABLE tickets IS 'Tickets table with fixed schema fields as columns and custom_data JSONB for dynamic fields';
COMMENT ON COLUMN tickets.id IS 'Auto-increment primary key';
COMMENT ON COLUMN tickets.ticket_id IS 'Unique ticket identifier with format TKT-{timestamp}';
COMMENT ON COLUMN tickets.custom_data IS 'JSONB field storing custom/dynamic fields';
COMMENT ON COLUMN tickets.createdtime IS 'Unix timestamp in milliseconds when ticket was created';
COMMENT ON COLUMN tickets.updatedtime IS 'Unix timestamp in milliseconds when ticket was last updated';
COMMENT ON COLUMN tickets.requesterid IS 'ID of user who requested the ticket';
COMMENT ON COLUMN tickets.technicianid IS 'ID of technician assigned to the ticket';
COMMENT ON COLUMN tickets.statusid IS 'Current status of the ticket';
COMMENT ON COLUMN tickets.priorityid IS 'Priority level of the ticket';
COMMENT ON COLUMN tickets.dueby IS 'Unix timestamp when ticket is due for resolution';

-- Helper function to get list of fixed field column names
CREATE OR REPLACE FUNCTION get_fixed_field_columns()
    RETURNS TEXT[] AS $$
BEGIN
    RETURN ARRAY[
        'id', 'ticket_id', 'created_at', 'updated_at',
        'updatedbyid', 'createdbyid', 'removedbyid', 'requesterid', 'technicianid', 'closedby', 'resolvedby',
        'updatedtime', 'createdtime', 'removedtime', 'dueby', 'firstresponsetime', 'lastclosedtime',
        'lastopenedtime', 'lastresolvedtime', 'lastviolationtime', 'olddueby', 'oldresponsedue',
        'resolutionescalationtime', 'responsedue', 'responseescalationtime', 'statuschangedtime',
        'groupchangedtime', 'lastolaviolationtime', 'oladueby', 'oldoladueby', 'askfeedbackdate',
        'firstfeedbackdate', 'olaescalationtime', 'lastucviolationtime', 'olducdueby', 'ucdueby',
        'ucescalationtime', 'lastapproveddate', 'name', 'oobtype', 'description', 'originaldescription',
        'subject', 'callfrom', 'emailreadconfigemail', 'removed', 'duetimemanuallyupdated', 'reopened',
        'responsedueviolated', 'slaviolated', 'purchaserequest', 'spam', 'viprequest', 'olaviolated',
        'ucviolated', 'migrated', 'categoryid', 'departmentid', 'groupid', 'impactid', 'locationid',
        'priorityid', 'statusid', 'urgencyid', 'violatedslaid', 'servicecatalogid', 'sourceid',
        'requesttype', 'suggestedcategoryid', 'suggestedgroupid', 'companyid', 'vendorid',
        'violateducid', 'transitionmodelid', 'messengerconfigid', 'approvalstatus', 'approvaltype',
        'resolutionduelevel', 'responseduelevel', 'supportlevel', 'oladuelevel', 'ucduelevel',
        'totalonholdduration', 'totalresolutiontime', 'totalslapausetime', 'totalworkingtime',
        'totaluconholdduration', 'totalucpausetime', 'totalucworkingtime', 'totalucresolutiontime',
        'templateid', 'emailreadconfigid'
        ];
END;
$$ LANGUAGE plpgsql IMMUTABLE;

-- Helper function to check if a field is a fixed column
CREATE OR REPLACE FUNCTION is_fixed_field(field_name TEXT)
    RETURNS BOOLEAN AS $$
BEGIN
    RETURN field_name = ANY(get_fixed_field_columns());
END;
$$ LANGUAGE plpgsql IMMUTABLE;