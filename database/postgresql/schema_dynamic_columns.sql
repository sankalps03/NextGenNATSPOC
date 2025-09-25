-- PostgreSQL Schema for Ticket Management System with Dynamic Column Mapping
-- Uses static base columns + 50 string columns + 50 numeric columns
-- Custom fields are mapped to available columns per category
-- Created: 2025-09-15

-- Drop tables if they exist (for clean recreation)
DROP TABLE IF EXISTS field_mappings CASCADE;
DROP TABLE IF EXISTS tickets_dynamic CASCADE;

-- Create tickets table with static base columns + dynamic columns
CREATE TABLE tickets_dynamic (
    -- Primary key with auto-increment
    id BIGSERIAL PRIMARY KEY,

    -- Core fields for protobuf compatibility (managed by application)
    ticket_id VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    -- User and assignment fields (from hybrid schema)
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
    requesttype VARCHAR(255),
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

    -- 50 String columns for dynamic field mapping
    c1_string TEXT,
    c2_string TEXT,
    c3_string TEXT,
    c4_string TEXT,
    c5_string TEXT,
    c6_string TEXT,
    c7_string TEXT,
    c8_string TEXT,
    c9_string TEXT,
    c10_string TEXT,
    c11_string TEXT,
    c12_string TEXT,
    c13_string TEXT,
    c14_string TEXT,
    c15_string TEXT,
    c16_string TEXT,
    c17_string TEXT,
    c18_string TEXT,
    c19_string TEXT,
    c20_string TEXT,
    c21_string TEXT,
    c22_string TEXT,
    c23_string TEXT,
    c24_string TEXT,
    c25_string TEXT,
    c26_string TEXT,
    c27_string TEXT,
    c28_string TEXT,
    c29_string TEXT,
    c30_string TEXT,
    c31_string TEXT,
    c32_string TEXT,
    c33_string TEXT,
    c34_string TEXT,
    c35_string TEXT,
    c36_string TEXT,
    c37_string TEXT,
    c38_string TEXT,
    c39_string TEXT,
    c40_string TEXT,
    c41_string TEXT,
    c42_string TEXT,
    c43_string TEXT,
    c44_string TEXT,
    c45_string TEXT,
    c46_string TEXT,
    c47_string TEXT,
    c48_string TEXT,
    c49_string TEXT,
    c50_string TEXT,

    -- 50 Numeric columns for dynamic field mapping
    c1_numeric BIGINT,
    c2_numeric BIGINT,
    c3_numeric BIGINT,
    c4_numeric BIGINT,
    c5_numeric BIGINT,
    c6_numeric BIGINT,
    c7_numeric BIGINT,
    c8_numeric BIGINT,
    c9_numeric BIGINT,
    c10_numeric BIGINT,
    c11_numeric BIGINT,
    c12_numeric BIGINT,
    c13_numeric BIGINT,
    c14_numeric BIGINT,
    c15_numeric BIGINT,
    c16_numeric BIGINT,
    c17_numeric BIGINT,
    c18_numeric BIGINT,
    c19_numeric BIGINT,
    c20_numeric BIGINT,
    c21_numeric BIGINT,
    c22_numeric BIGINT,
    c23_numeric BIGINT,
    c24_numeric BIGINT,
    c25_numeric BIGINT,
    c26_numeric BIGINT,
    c27_numeric BIGINT,
    c28_numeric BIGINT,
    c29_numeric BIGINT,
    c30_numeric BIGINT,
    c31_numeric BIGINT,
    c32_numeric BIGINT,
    c33_numeric BIGINT,
    c34_numeric BIGINT,
    c35_numeric BIGINT,
    c36_numeric BIGINT,
    c37_numeric BIGINT,
    c38_numeric BIGINT,
    c39_numeric BIGINT,
    c40_numeric BIGINT,
    c41_numeric BIGINT,
    c42_numeric BIGINT,
    c43_numeric BIGINT,
    c44_numeric BIGINT,
    c45_numeric BIGINT,
    c46_numeric BIGINT,
    c47_numeric BIGINT,
    c48_numeric BIGINT,
    c49_numeric BIGINT,
    c50_numeric BIGINT,

    -- Array columns for dynamic field mapping
    c1_array_string TEXT[],
    c2_array_string TEXT[],

    c1_array_numeric BIGINT[],
    c2_array_numeric BIGINT[],

    -- Geo location columns for spatial data
    c1_geolocation POINT,
    c2_geolocation POINT
);

-- Create field mappings table to track which fields map to which columns per category
CREATE TABLE field_mappings (
    id BIGSERIAL PRIMARY KEY,
    category_id BIGINT NOT NULL,
    field_name VARCHAR(255) NOT NULL,
    column_name VARCHAR(50) NOT NULL,
    data_type VARCHAR(20) NOT NULL, -- 'string', 'numeric', 'array_string', 'array_numeric', or 'geolocation'
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    -- Ensure unique mapping per category and field
    UNIQUE(category_id, field_name),
    -- Ensure unique column assignment per category and column
    UNIQUE(category_id, column_name)
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
CREATE TRIGGER update_tickets_dynamic_updated_at 
    BEFORE UPDATE ON tickets_dynamic 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

-- Essential indexes for primary lookups
CREATE UNIQUE INDEX idx_tickets_dynamic_ticket_id ON tickets_dynamic(ticket_id);
CREATE INDEX idx_tickets_dynamic_created_at ON tickets_dynamic(created_at);
CREATE INDEX idx_tickets_dynamic_createdtime ON tickets_dynamic(createdtime);

-- Core business logic indexes
CREATE INDEX idx_tickets_dynamic_requester_status ON tickets_dynamic(requesterid, statusid);
CREATE INDEX idx_tickets_dynamic_technician_status ON tickets_dynamic(technicianid, statusid);
CREATE INDEX idx_tickets_dynamic_priority_status ON tickets_dynamic(priorityid, statusid);
CREATE INDEX idx_tickets_dynamic_company_category ON tickets_dynamic(companyid, categoryid);
CREATE INDEX idx_tickets_dynamic_group_department ON tickets_dynamic(groupid, departmentid);
CREATE INDEX idx_tickets_dynamic_due_status ON tickets_dynamic(dueby, statusid) WHERE dueby IS NOT NULL;

-- Performance indexes for common queries
CREATE INDEX idx_tickets_dynamic_status_created ON tickets_dynamic(statusid, createdtime);
CREATE INDEX idx_tickets_dynamic_requester_created ON tickets_dynamic(requesterid, createdtime);
CREATE INDEX idx_tickets_dynamic_company_status_priority ON tickets_dynamic(companyid, statusid, priorityid);
CREATE INDEX idx_tickets_dynamic_not_spam_not_removed ON tickets_dynamic(statusid, priorityid) WHERE NOT spam AND NOT removed;

-- Text search index for subject and description
CREATE INDEX idx_tickets_dynamic_text_search ON tickets_dynamic USING GIN (to_tsvector('english', COALESCE(subject, '') || ' ' || COALESCE(description, '')));

-- Indexes for field mappings table
CREATE INDEX idx_field_mappings_category ON field_mappings(category_id);
CREATE INDEX idx_field_mappings_field_name ON field_mappings(field_name);
CREATE INDEX idx_field_mappings_column_name ON field_mappings(column_name);

-- Indexes for array columns (using GIN for array operations)
CREATE INDEX idx_tickets_dynamic_c1_array_string ON tickets_dynamic USING GIN (c1_array_string);
CREATE INDEX idx_tickets_dynamic_c2_array_string ON tickets_dynamic USING GIN (c2_array_string);
CREATE INDEX idx_tickets_dynamic_c1_array_numeric ON tickets_dynamic USING GIN (c1_array_numeric);
CREATE INDEX idx_tickets_dynamic_c2_array_numeric ON tickets_dynamic USING GIN (c2_array_numeric);

-- Indexes for geo location columns (using GIST for spatial operations)
CREATE INDEX idx_tickets_dynamic_c1_geolocation ON tickets_dynamic USING GIST (c1_geolocation);
CREATE INDEX idx_tickets_dynamic_c2_geolocation ON tickets_dynamic USING GIST (c2_geolocation);

-- Helper function to get list of static/fixed field column names
CREATE OR REPLACE FUNCTION get_static_field_columns()
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

-- Helper function to check if a field is a static column
CREATE OR REPLACE FUNCTION is_static_field(field_name TEXT)
    RETURNS BOOLEAN AS $$
BEGIN
    RETURN field_name = ANY(get_static_field_columns());
END;
$$ LANGUAGE plpgsql IMMUTABLE;

-- Helper function to get list of all dynamic column names (string and numeric)
CREATE OR REPLACE FUNCTION get_dynamic_column_names()
    RETURNS TEXT[] AS $$
BEGIN
    RETURN ARRAY[
        -- String columns
        'c1_string', 'c2_string', 'c3_string', 'c4_string', 'c5_string', 'c6_string', 'c7_string', 'c8_string', 'c9_string', 'c10_string',
        'c11_string', 'c12_string', 'c13_string', 'c14_string', 'c15_string', 'c16_string', 'c17_string', 'c18_string', 'c19_string', 'c20_string',
        'c21_string', 'c22_string', 'c23_string', 'c24_string', 'c25_string', 'c26_string', 'c27_string', 'c28_string', 'c29_string', 'c30_string',
        'c31_string', 'c32_string', 'c33_string', 'c34_string', 'c35_string', 'c36_string', 'c37_string', 'c38_string', 'c39_string', 'c40_string',
        'c41_string', 'c42_string', 'c43_string', 'c44_string', 'c45_string', 'c46_string', 'c47_string', 'c48_string', 'c49_string', 'c50_string',
        -- Numeric columns
        'c1_numeric', 'c2_numeric', 'c3_numeric', 'c4_numeric', 'c5_numeric', 'c6_numeric', 'c7_numeric', 'c8_numeric', 'c9_numeric', 'c10_numeric',
        'c11_numeric', 'c12_numeric', 'c13_numeric', 'c14_numeric', 'c15_numeric', 'c16_numeric', 'c17_numeric', 'c18_numeric', 'c19_numeric', 'c20_numeric',
        'c21_numeric', 'c22_numeric', 'c23_numeric', 'c24_numeric', 'c25_numeric', 'c26_numeric', 'c27_numeric', 'c28_numeric', 'c29_numeric', 'c30_numeric',
        'c31_numeric', 'c32_numeric', 'c33_numeric', 'c34_numeric', 'c35_numeric', 'c36_numeric', 'c37_numeric', 'c38_numeric', 'c39_numeric', 'c40_numeric',
        'c41_numeric', 'c42_numeric', 'c43_numeric', 'c44_numeric', 'c45_numeric', 'c46_numeric', 'c47_numeric', 'c48_numeric', 'c49_numeric', 'c50_numeric',
        -- Array columns
        'c1_array_string', 'c2_array_string',
        'c1_array_numeric', 'c2_array_numeric',
        -- Geo location columns
        'c1_geolocation', 'c2_geolocation'
        ];
END;
$$ LANGUAGE plpgsql IMMUTABLE;

-- Comments for documentation
COMMENT ON TABLE tickets_dynamic IS 'Tickets table with comprehensive static fields from hybrid schema and dynamic column mapping for custom fields';
COMMENT ON TABLE field_mappings IS 'Maps custom field names to dynamic columns per category';
COMMENT ON COLUMN tickets_dynamic.id IS 'Auto-increment primary key';
COMMENT ON COLUMN tickets_dynamic.ticket_id IS 'Unique ticket identifier for protobuf compatibility';
COMMENT ON COLUMN tickets_dynamic.createdtime IS 'Unix timestamp in milliseconds when ticket was created';
COMMENT ON COLUMN tickets_dynamic.updatedtime IS 'Unix timestamp in milliseconds when ticket was last updated';
COMMENT ON COLUMN tickets_dynamic.requesterid IS 'ID of user who requested the ticket';
COMMENT ON COLUMN tickets_dynamic.technicianid IS 'ID of technician assigned to the ticket';
COMMENT ON COLUMN tickets_dynamic.statusid IS 'Current status of the ticket';
COMMENT ON COLUMN tickets_dynamic.priorityid IS 'Priority level of the ticket';
COMMENT ON COLUMN tickets_dynamic.dueby IS 'Unix timestamp when ticket is due for resolution';
COMMENT ON COLUMN field_mappings.category_id IS 'Category ID that this field mapping belongs to';
COMMENT ON COLUMN field_mappings.field_name IS 'Original field name from the ticket data';
COMMENT ON COLUMN field_mappings.column_name IS 'Mapped column name (e.g., c1_string, c5_numeric)';
COMMENT ON COLUMN field_mappings.data_type IS 'Data type of the field (string, numeric, array_string, array_numeric, or geolocation)';

-- Comments for new column types
COMMENT ON COLUMN tickets_dynamic.c1_array_string IS 'Dynamic array column for string array fields';
COMMENT ON COLUMN tickets_dynamic.c2_array_string IS 'Dynamic array column for string array fields';
COMMENT ON COLUMN tickets_dynamic.c1_array_numeric IS 'Dynamic array column for numeric array fields';
COMMENT ON COLUMN tickets_dynamic.c2_array_numeric IS 'Dynamic array column for numeric array fields';
COMMENT ON COLUMN tickets_dynamic.c1_geolocation IS 'Dynamic geo location column for spatial coordinates (latitude, longitude)';
COMMENT ON COLUMN tickets_dynamic.c2_geolocation IS 'Dynamic geo location column for spatial coordinates (latitude, longitude)';
