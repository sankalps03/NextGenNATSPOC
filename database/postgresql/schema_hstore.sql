-- PostgreSQL Hstore Schema for Ticket Management System
-- Uses hstore extension for flexible field storage
-- Created: 2025-09-12

-- Enable hstore extension if not already enabled
CREATE EXTENSION IF NOT EXISTS hstore;

-- Drop table if exists (for clean recreation)
DROP TABLE IF EXISTS tickets_hstore CASCADE;

-- Create tickets table with hstore for dynamic fields
CREATE TABLE tickets_hstore (
    -- Primary key with auto-increment
    id BIGSERIAL PRIMARY KEY,

    -- Core metadata fields (outside hstore)
    ticket_id VARCHAR(255) UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    -- Dynamic fields stored in hstore
    fields HSTORE NOT NULL DEFAULT ''::hstore
);

-- CLUSTERED INDEXES - Grouped by Business Domain (Hstore Implementation)
-- Replaces individual field indexes to reduce write burden and improve query performance
-- Based on the same clustering strategy as the traditional PostgreSQL schema

-- Essential individual indexes for primary lookups
CREATE UNIQUE INDEX idx_tickets_hstore_ticket_id ON tickets_hstore(ticket_id);
CREATE INDEX idx_tickets_hstore_created_at ON tickets_hstore(created_at);
CREATE INDEX idx_tickets_hstore_updated_at ON tickets_hstore(updated_at);

-- GIN index on hstore fields for efficient querying of any field
-- This enables fast lookups on any field within the hstore
CREATE INDEX idx_tickets_hstore_fields_gin ON tickets_hstore USING GIN (fields);

-- Comments for documentation
COMMENT ON TABLE tickets_hstore IS 'Ticket storage using PostgreSQL hstore with clustered indexing strategy for optimal performance';
COMMENT ON COLUMN tickets_hstore.id IS 'Auto-increment primary key for internal database operations';
COMMENT ON COLUMN tickets_hstore.ticket_id IS 'Unique ticket identifier for external references and API operations';
COMMENT ON COLUMN tickets_hstore.created_at IS 'Timestamp when the ticket was created (managed by application)';
COMMENT ON COLUMN tickets_hstore.updated_at IS 'Timestamp when the ticket was last updated (managed by application)';
COMMENT ON COLUMN tickets_hstore.fields IS 'Hstore containing all dynamic ticket fields with clustered indexing for performance';

-- Indexing Strategy Notes:
-- 1. Clustered indexes group related fields by business domain to reduce write burden
-- 2. GIN index provides fast lookup capability for any hstore field
-- 3. Composite indexes optimize common multi-field query patterns
-- 4. Index design mirrors traditional schema for consistent performance characteristics

-- Example hstore field structure (for documentation):
-- fields might contain:
-- 'title=>Bug Report,description=>System crash,priorityid=>1,statusid=>2,requesterid=>123,technicianid=>456,groupid=>789'

-- Performance Benefits:
-- - Reduced index maintenance overhead compared to individual field indexes
-- - Optimized for common query patterns (status+priority, requester+status, etc.)
-- - Efficient range queries within business domain clusters
-- - Balanced read/write performance for high-throughput scenarios
