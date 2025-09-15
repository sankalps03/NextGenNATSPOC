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

    -- Static base columns (as requested by user)
    -- Note: timestamp fields stored as BIGINT to handle Unix timestamps from generators
    id_field VARCHAR(255), -- renamed to avoid conflict with primary key
    name VARCHAR(255),
    createdbyid BIGINT,
    createdtime BIGINT, -- Unix timestamp in milliseconds
    updatedbyid BIGINT,
    updatedtime BIGINT, -- Unix timestamp in milliseconds
    statusid BIGINT,
    priorityid BIGINT,
    requesterid BIGINT,
    subject VARCHAR(500),

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
    c50_numeric BIGINT
);

-- Create field mappings table to track which fields map to which columns per category
CREATE TABLE field_mappings (
    id BIGSERIAL PRIMARY KEY,
    category_id BIGINT NOT NULL,
    field_name VARCHAR(255) NOT NULL,
    column_name VARCHAR(50) NOT NULL,
    data_type VARCHAR(20) NOT NULL, -- 'string' or 'numeric'
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
CREATE INDEX idx_tickets_dynamic_category_status ON tickets_dynamic(statusid, priorityid);
CREATE INDEX idx_tickets_dynamic_requester ON tickets_dynamic(requesterid);
CREATE INDEX idx_tickets_dynamic_technician ON tickets_dynamic(createdbyid);

-- Indexes for field mappings table
CREATE INDEX idx_field_mappings_category ON field_mappings(category_id);
CREATE INDEX idx_field_mappings_field_name ON field_mappings(field_name);
CREATE INDEX idx_field_mappings_column_name ON field_mappings(column_name);

-- Comments for documentation
COMMENT ON TABLE tickets_dynamic IS 'Tickets table with static base columns and dynamic column mapping for custom fields';
COMMENT ON TABLE field_mappings IS 'Maps custom field names to dynamic columns per category';
COMMENT ON COLUMN tickets_dynamic.id IS 'Auto-increment primary key';
COMMENT ON COLUMN tickets_dynamic.ticket_id IS 'Unique ticket identifier for protobuf compatibility';
COMMENT ON COLUMN field_mappings.category_id IS 'Category ID that this field mapping belongs to';
COMMENT ON COLUMN field_mappings.field_name IS 'Original field name from the ticket data';
COMMENT ON COLUMN field_mappings.column_name IS 'Mapped column name (e.g., c1_string, c5_numeric)';
COMMENT ON COLUMN field_mappings.data_type IS 'Data type of the field (string or numeric)';
