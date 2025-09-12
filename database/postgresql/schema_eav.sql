-- PostgreSQL EAV Schema for Ticket Management System
-- Entity-Attribute-Value design for flexible schema
-- Created: 2025-09-12

-- Drop table if exists (for clean recreation)
DROP TABLE IF EXISTS ticket_eav CASCADE;

-- Create EAV table for ticket data
CREATE TABLE ticket_eav (
    -- Primary key with auto-increment
    id BIGSERIAL PRIMARY KEY,

    -- Entity identification
    entity_id VARCHAR(255) NOT NULL,  -- ticket_id

    -- Attribute definition
    attribute_id SMALLINT NOT NULL,        -- numeric field ID (mapped in memory)

    -- Value storage columns - only one should be populated per row
    string_value TEXT,                -- for VARCHAR, TEXT, and string data
    int_value BIGINT,                -- for BIGINT, INTEGER, and timestamp data
    boolean_value BOOLEAN,           -- for BOOLEAN data

    -- Metadata
    datatype SMALLINT NOT NULL,      -- 1=string, 2=int, 3=boolean

    -- Timestamps
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- No updated_at column, so no trigger needed

-- Indexes for performance
-- Primary lookup index - find all attributes for a specific ticket
CREATE INDEX idx_ticket_eav_entity ON ticket_eav(entity_id);

-- Attribute lookup index - find all tickets with specific attribute
CREATE INDEX idx_ticket_eav_attribute ON ticket_eav(attribute_id);

-- Value lookup indexes for search operations
CREATE INDEX idx_ticket_eav_string_value ON ticket_eav(attribute_id, string_value)
    WHERE datatype = 1 AND string_value IS NOT NULL;

CREATE INDEX idx_ticket_eav_int_value ON ticket_eav(attribute_id, int_value)
    WHERE datatype = 2 AND int_value IS NOT NULL;

CREATE INDEX idx_ticket_eav_boolean_value ON ticket_eav(attribute_id, boolean_value)
    WHERE datatype = 3 AND boolean_value IS NOT NULL;

-- Composite index for efficient queries
CREATE INDEX idx_ticket_eav_entity_attr ON ticket_eav(entity_id, attribute_id);

-- Unique constraint to prevent duplicate attributes per entity
CREATE UNIQUE INDEX idx_ticket_eav_unique_attr ON ticket_eav(entity_id, attribute_id);

-- Comments for documentation
COMMENT ON TABLE ticket_eav IS 'EAV table for flexible ticket data storage';
COMMENT ON COLUMN ticket_eav.entity_id IS 'Ticket ID that this attribute belongs to';
COMMENT ON COLUMN ticket_eav.attribute_id IS 'Numeric field ID mapped to field names in memory';
COMMENT ON COLUMN ticket_eav.string_value IS 'String/text value storage';
COMMENT ON COLUMN ticket_eav.int_value IS 'Integer/timestamp value storage';
COMMENT ON COLUMN ticket_eav.boolean_value IS 'Boolean value storage';
COMMENT ON COLUMN ticket_eav.datatype IS 'Data type indicator: 1=string, 2=int, 3=boolean';
COMMENT ON COLUMN ticket_eav.created_at IS 'Timestamp when the EAV row was created';
