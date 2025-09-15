-- PostgreSQL DocumentDB Hybrid Schema for Ticket Management System
-- Combines fixed schema for common fields with BSON for dynamic fields
-- Created: 2025-09-12

-- Grant necessary permissions to current user/role

-- Create uuid extension for generating unique IDs
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Verify installation
SELECT extname, extversion FROM pg_extension WHERE extname = 'uuid-ossp';

GRANT ALL ON SCHEMA public TO PUBLIC;

-- Drop table if exists (for clean recreation)
DROP TABLE IF EXISTS ticket_hybrid CASCADE;

-- Create hybrid table with fixed schema + dynamic BSON fields
CREATE TABLE ticket_hybrid (
    -- DocumentDB style primary key
    _id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- Core fields for protobuf compatibility (always fixed schema)
    ticket_id VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    -- Fixed schema: High-frequency query fields (most commonly searched/filtered)
    -- User and assignment fields
    requesterid BIGINT,
    technicianid BIGINT,
    createdbyid BIGINT,

    -- Status and priority (most common filters)
    statusid BIGINT,
    priorityid BIGINT,
    urgencyid BIGINT,

    -- Timing and SLA fields (frequently queried)
    createdtime BIGINT,
    updatedtime BIGINT,
    dueby BIGINT,

    -- Organization fields (common filters)
    companyid BIGINT,
    groupid BIGINT,
    departmentid BIGINT,
    categoryid BIGINT,

    -- Common text fields
    subject VARCHAR(500),
    description TEXT,

    -- Common boolean flags
    removed BOOLEAN DEFAULT FALSE,
    spam BOOLEAN DEFAULT FALSE,

    -- Dynamic fields stored as BSON (for flexibility)
    -- All other fields that are less frequently queried or custom fields
    dynamic_fields documentdb_core.bson DEFAULT '{}',

    -- Additional BSON fields for organized data
    user_fields documentdb_core.bson DEFAULT '{}',      -- User-related dynamic data
    timing_fields documentdb_core.bson DEFAULT '{}',    -- Timing/SLA dynamic data
    workflow_fields documentdb_core.bson DEFAULT '{}',  -- Workflow/approval dynamic data
    custom_fields documentdb_core.bson DEFAULT '{}',    -- Custom organization fields

    -- Metadata for BSON field management
    schema_version INTEGER DEFAULT 1,

    -- Constraint for ticket ID format
    CONSTRAINT chk_ticket_id_format CHECK (ticket_id ~ '^TKT-[0-9]+$')
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
CREATE TRIGGER update_ticket_hybrid_updated_at 
    BEFORE UPDATE ON ticket_hybrid 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

-- INDEXES for Hybrid Schema
-- Essential individual indexes for primary lookups
CREATE UNIQUE INDEX idx_ticket_hybrid_ticket_id ON ticket_hybrid(ticket_id);
CREATE INDEX idx_ticket_hybrid_created_at ON ticket_hybrid(created_at);
CREATE INDEX idx_ticket_hybrid_createdtime ON ticket_hybrid(createdtime);

-- Fixed schema indexes (high-frequency query fields)
CREATE INDEX idx_ticket_hybrid_requester_status ON ticket_hybrid(requesterid, statusid);
CREATE INDEX idx_ticket_hybrid_technician_status ON ticket_hybrid(technicianid, statusid);
CREATE INDEX idx_ticket_hybrid_priority_status ON ticket_hybrid(priorityid, statusid);
CREATE INDEX idx_ticket_hybrid_company_category ON ticket_hybrid(companyid, categoryid);
CREATE INDEX idx_ticket_hybrid_group_department ON ticket_hybrid(groupid, departmentid);
CREATE INDEX idx_ticket_hybrid_due_status ON ticket_hybrid(dueby, statusid) WHERE dueby IS NOT NULL;

-- -- BSON indexes for dynamic fields (enables efficient queries on BSON content)
-- CREATE INDEX idx_ticket_hybrid_dynamic_fields_gin ON ticket_hybrid USING GIN (dynamic_fields);
-- CREATE INDEX idx_ticket_hybrid_user_fields_gin ON ticket_hybrid USING GIN (user_fields);
-- CREATE INDEX idx_ticket_hybrid_timing_fields_gin ON ticket_hybrid USING GIN (timing_fields);
-- CREATE INDEX idx_ticket_hybrid_workflow_fields_gin ON ticket_hybrid USING GIN (workflow_fields);
-- CREATE INDEX idx_ticket_hybrid_custom_fields_gin ON ticket_hybrid USING GIN (custom_fields);
--
-- -- Specialized BSON indexes for common field patterns
-- -- Note: Using text extraction for BSON fields and casting separately
-- CREATE INDEX idx_ticket_hybrid_dynamic_btree ON ticket_hybrid
--     USING btree (((dynamic_fields->>'priority')::int));
-- CREATE INDEX idx_ticket_hybrid_user_btree ON ticket_hybrid
--     USING btree (((user_fields->>'assignee_id')::bigint));
-- CREATE INDEX idx_ticket_hybrid_timing_btree ON ticket_hybrid
--     USING btree (((timing_fields->>'sla_due')::bigint));
--
-- -- Text search index for subject and description
-- CREATE INDEX idx_ticket_hybrid_text_search ON ticket_hybrid USING GIN (to_tsvector('english', COALESCE(subject, '') || ' ' || COALESCE(description, '')));

-- Composite indexes for common query patterns
CREATE INDEX idx_ticket_hybrid_status_created ON ticket_hybrid(statusid, createdtime);
CREATE INDEX idx_ticket_hybrid_requester_created ON ticket_hybrid(requesterid, createdtime);
CREATE INDEX idx_ticket_hybrid_company_status_priority ON ticket_hybrid(companyid, statusid, priorityid);
CREATE INDEX idx_ticket_hybrid_not_spam_not_removed ON ticket_hybrid(statusid, priorityid) WHERE NOT spam AND NOT removed;

-- Comments for documentation
COMMENT ON TABLE ticket_hybrid IS 'Hybrid tickets table combining fixed schema for common fields with BSON for dynamic fields';
COMMENT ON COLUMN ticket_hybrid._id IS 'DocumentDB style BSON ObjectId primary key';
COMMENT ON COLUMN ticket_hybrid.ticket_id IS 'Unique ticket identifier with format TKT-{timestamp}';
COMMENT ON COLUMN ticket_hybrid.dynamic_fields IS 'BSON field storing general dynamic fields';
COMMENT ON COLUMN ticket_hybrid.user_fields IS 'BSON field storing user-related dynamic data';
COMMENT ON COLUMN ticket_hybrid.timing_fields IS 'BSON field storing timing/SLA dynamic data';
COMMENT ON COLUMN ticket_hybrid.workflow_fields IS 'BSON field storing workflow/approval dynamic data';
COMMENT ON COLUMN ticket_hybrid.custom_fields IS 'BSON field storing custom organization fields';
COMMENT ON COLUMN ticket_hybrid.schema_version IS 'Version of the dynamic schema for migration purposes';
COMMENT ON COLUMN ticket_hybrid.createdtime IS 'Unix timestamp in milliseconds when ticket was created';
COMMENT ON COLUMN ticket_hybrid.updatedtime IS 'Unix timestamp in milliseconds when ticket was last updated';
COMMENT ON COLUMN ticket_hybrid.requesterid IS 'ID of user who requested the ticket';
COMMENT ON COLUMN ticket_hybrid.technicianid IS 'ID of technician assigned to the ticket';
COMMENT ON COLUMN ticket_hybrid.statusid IS 'Current status of the ticket';
COMMENT ON COLUMN ticket_hybrid.priorityid IS 'Priority level of the ticket';
COMMENT ON COLUMN ticket_hybrid.dueby IS 'Unix timestamp when ticket is due for resolution';

-- Create helper function for BSON field extraction
CREATE OR REPLACE FUNCTION get_bson_field(
    bson_data documentdb_core.bson,
    field_name TEXT,
    field_type TEXT DEFAULT 'string'
) RETURNS TEXT AS $$
BEGIN
    CASE field_type
        WHEN 'string' THEN
            RETURN bson_data->>field_name;
        WHEN 'number' THEN
            RETURN bson_data->>field_name;
        WHEN 'boolean' THEN
            RETURN bson_data->>field_name;
        ELSE
            RETURN bson_data->>field_name;
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

-- Create helper function for setting BSON fields
CREATE OR REPLACE FUNCTION set_bson_field(
    bson_data documentdb_core.bson,
    field_name TEXT,
    field_value TEXT,
    field_type TEXT DEFAULT 'string'
) RETURNS documentdb_core.bson AS $$
DECLARE
    result_bson documentdb_core.bson;
BEGIN
    -- Initialize empty BSON if null
    IF bson_data IS NULL THEN
        bson_data := '{}'::documentdb_core.bson;
    END IF;
    
    -- Simplified approach using JSON functions that work with BSON
    CASE field_type
        WHEN 'string' THEN
            result_bson := to_bson(json_build_object(field_name, field_value)::text);
        WHEN 'number' THEN
            result_bson := to_bson(json_build_object(field_name, field_value::NUMERIC)::text);
        WHEN 'boolean' THEN
            result_bson := to_bson(json_build_object(field_name, field_value::BOOLEAN)::text);
        ELSE
            result_bson := to_bson(json_build_object(field_name, field_value)::text);
    END CASE;
    
    RETURN result_bson;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

-- Create helper function for BSON field operations
CREATE OR REPLACE FUNCTION merge_bson_fields(
    target_bson documentdb_core.bson,
    source_bson documentdb_core.bson
) RETURNS documentdb_core.bson AS $$
BEGIN
    IF target_bson IS NULL THEN
        RETURN COALESCE(source_bson, '{}'::bson);
    END IF;
    
    IF source_bson IS NULL THEN
        RETURN target_bson;
    END IF;
    
    -- Simplified merge using JSON operations
    RETURN to_bson((from_bson(target_bson)::jsonb || from_bson(source_bson)::jsonb)::text);
END;
$$ LANGUAGE plpgsql IMMUTABLE;