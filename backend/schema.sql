-- ─── ECMS Database Schema ─────────────────────────────────────────────────────
-- Docker creates 'ecms' automatically via POSTGRES_DB env var.

-- ── Document Class (FileNet: Document Class) ──────────────────────────────────
-- Defines a type of document (e.g. "Invoice", "Contract", "Engineering Drawing")
-- Classes can inherit from a parent class (single-level hierarchy)
CREATE TABLE IF NOT EXISTS document_classes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT DEFAULT '',
    parent_id   UUID REFERENCES document_classes(id) ON DELETE SET NULL,
    icon        TEXT DEFAULT '📄',
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- ── Property Template (FileNet: Property Template) ────────────────────────────
-- Defines a reusable typed property (e.g. "Invoice Date", "Contract Value")
CREATE TABLE IF NOT EXISTS property_templates (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL UNIQUE,
    display_name   TEXT NOT NULL,
    data_type      TEXT NOT NULL CHECK (data_type IN ('string','integer','decimal','boolean','date','datetime','user')),
    is_required    BOOLEAN DEFAULT FALSE,
    is_multivalued BOOLEAN DEFAULT FALSE,
    default_value  TEXT,
    choices        TEXT[],          -- for enum-style constrained string values
    description    TEXT DEFAULT '',
    created_at     TIMESTAMPTZ DEFAULT NOW()
);

-- ── Class-Property Mapping (FileNet: Class Property Assignment) ───────────────
-- Links property templates to document classes; overrides required/default per class
CREATE TABLE IF NOT EXISTS class_property_assignments (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    class_id             UUID NOT NULL REFERENCES document_classes(id) ON DELETE CASCADE,
    property_template_id UUID NOT NULL REFERENCES property_templates(id) ON DELETE CASCADE,
    is_required          BOOLEAN,          -- overrides template default per class
    default_value        TEXT,             -- overrides template default per class
    sort_order           INTEGER DEFAULT 0,
    UNIQUE (class_id, property_template_id)
);

-- ── Documents ─────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS documents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    description TEXT DEFAULT '',
    class_id    UUID REFERENCES document_classes(id) ON DELETE SET NULL,
    category    TEXT DEFAULT 'General',   -- kept for backward compat
    tags        TEXT[] DEFAULT '{}',
    s3_key      TEXT NOT NULL,
    s3_bucket   TEXT NOT NULL,
    file_size   BIGINT DEFAULT 0,
    mime_type   TEXT DEFAULT '',
    version     INTEGER DEFAULT 1,
    status      TEXT DEFAULT 'active' CHECK (status IN ('active','archived','deleted')),
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW(),
    created_by  TEXT DEFAULT 'system'
);

-- ── Document Property Values (FileNet: Property Values) ───────────────────────
-- Stores the actual values of class properties for each document
CREATE TABLE IF NOT EXISTS document_property_values (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id          UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    property_template_id UUID NOT NULL REFERENCES property_templates(id) ON DELETE CASCADE,
    value_string         TEXT,
    value_integer        BIGINT,
    value_decimal        NUMERIC(18,6),
    value_boolean        BOOLEAN,
    value_date           DATE,
    value_datetime       TIMESTAMPTZ,
    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW()
);

-- ── Document Versions ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS document_versions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID REFERENCES documents(id) ON DELETE CASCADE,
    version     INTEGER NOT NULL,
    s3_key      TEXT NOT NULL,
    file_size   BIGINT DEFAULT 0,
    uploaded_at TIMESTAMPTZ DEFAULT NOW(),
    comment     TEXT DEFAULT '',
    UNIQUE (document_id, version)
);

-- ── Audit Log ─────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS audit_log (
    id          SERIAL PRIMARY KEY,
    document_id UUID,
    action      TEXT NOT NULL,
    actor       TEXT DEFAULT 'system',
    details     JSONB,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

-- ── Indexes ───────────────────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_documents_status        ON documents(status);
CREATE INDEX IF NOT EXISTS idx_documents_class         ON documents(class_id);
CREATE INDEX IF NOT EXISTS idx_documents_category      ON documents(category);
CREATE INDEX IF NOT EXISTS idx_documents_tags          ON documents USING GIN(tags);
CREATE INDEX IF NOT EXISTS idx_documents_created       ON documents(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_prop_values_document    ON document_property_values(document_id);
CREATE INDEX IF NOT EXISTS idx_prop_values_template    ON document_property_values(property_template_id);
CREATE INDEX IF NOT EXISTS idx_class_props_class       ON class_property_assignments(class_id);
CREATE INDEX IF NOT EXISTS idx_audit_document          ON audit_log(document_id);

-- ── Auto-update trigger ────────────────────────────────────────────────────────
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN NEW.updated_at = NOW(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS set_updated_at ON documents;
CREATE TRIGGER set_updated_at BEFORE UPDATE ON documents
FOR EACH ROW EXECUTE FUNCTION update_updated_at();

DROP TRIGGER IF EXISTS set_updated_at ON document_classes;
CREATE TRIGGER set_updated_at BEFORE UPDATE ON document_classes
FOR EACH ROW EXECUTE FUNCTION update_updated_at();

DROP TRIGGER IF EXISTS set_updated_at ON document_property_values;
CREATE TRIGGER set_updated_at BEFORE UPDATE ON document_property_values
FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- ── Seed: built-in document classes ───────────────────────────────────────────
INSERT INTO document_classes (id, name, description, icon) VALUES
  ('00000000-0000-0000-0000-000000000001', 'Document',          'Base document class',           '📄'),
  ('00000000-0000-0000-0000-000000000002', 'Contract',          'Legal contracts and agreements', '⚖️'),
  ('00000000-0000-0000-0000-000000000003', 'Invoice',           'Invoices and billing documents', '🧾'),
  ('00000000-0000-0000-0000-000000000004', 'Engineering Drawing','Technical drawings and schematics','📐'),
  ('00000000-0000-0000-0000-000000000005', 'HR Document',       'Human resources documents',     '👥'),
  ('00000000-0000-0000-0000-000000000006', 'Report',            'Business and technical reports', '📊')
ON CONFLICT (name) DO NOTHING;

-- ── Seed: built-in property templates ────────────────────────────────────────
INSERT INTO property_templates (id, name, display_name, data_type, is_required, description) VALUES
  ('10000000-0000-0000-0000-000000000001', 'author',          'Author',           'string',   false, 'Document author or creator'),
  ('10000000-0000-0000-0000-000000000002', 'effective_date',  'Effective Date',   'date',     false, 'Date the document takes effect'),
  ('10000000-0000-0000-0000-000000000003', 'expiry_date',     'Expiry Date',      'date',     false, 'Date the document expires'),
  ('10000000-0000-0000-0000-000000000004', 'contract_value',  'Contract Value',   'decimal',  false, 'Monetary value of the contract'),
  ('10000000-0000-0000-0000-000000000005', 'counterparty',    'Counterparty',     'string',   false, 'Other party in the contract'),
  ('10000000-0000-0000-0000-000000000006', 'invoice_number',  'Invoice Number',   'string',   true,  'Unique invoice identifier'),
  ('10000000-0000-0000-0000-000000000007', 'invoice_amount',  'Invoice Amount',   'decimal',  true,  'Total invoice amount'),
  ('10000000-0000-0000-0000-000000000008', 'vendor',          'Vendor',           'string',   false, 'Vendor or supplier name'),
  ('10000000-0000-0000-0000-000000000009', 'drawing_number',  'Drawing Number',   'string',   true,  'Engineering drawing reference number'),
  ('10000000-0000-0000-0000-000000000010', 'revision',        'Revision',         'string',   false, 'Document revision identifier'),
  ('10000000-0000-0000-0000-000000000011', 'department',      'Department',       'string',   false, 'Owning department'),
  ('10000000-0000-0000-0000-000000000012', 'employee_id',     'Employee ID',      'string',   false, 'Related employee identifier'),
  ('10000000-0000-0000-0000-000000000013', 'confidential',    'Confidential',     'boolean',  false, 'Whether the document is confidential'),
  ('10000000-0000-0000-0000-000000000014', 'report_period',   'Report Period',    'string',   false, 'The period this report covers')
ON CONFLICT (name) DO NOTHING;

-- ── Seed: assign properties to classes ───────────────────────────────────────
INSERT INTO class_property_assignments (class_id, property_template_id, sort_order) VALUES
  -- Contract
  ('00000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000005',1),  -- counterparty
  ('00000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000004',2),  -- contract_value
  ('00000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000002',3),  -- effective_date
  ('00000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000003',4),  -- expiry_date
  ('00000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000013',5),  -- confidential
  -- Invoice
  ('00000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000006',1),  -- invoice_number
  ('00000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000007',2),  -- invoice_amount
  ('00000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000008',3),  -- vendor
  ('00000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000002',4),  -- effective_date
  -- Engineering Drawing
  ('00000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000009',1),  -- drawing_number
  ('00000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000010',2),  -- revision
  ('00000000-0000-0000-0000-000000000004','10000000-0000-0000-0000-000000000001',3),  -- author
  -- HR Document
  ('00000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000012',1),  -- employee_id
  ('00000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000011',2),  -- department
  ('00000000-0000-0000-0000-000000000005','10000000-0000-0000-0000-000000000013',3),  -- confidential
  -- Report
  ('00000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000014',1),  -- report_period
  ('00000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000011',2),  -- department
  ('00000000-0000-0000-0000-000000000006','10000000-0000-0000-0000-000000000001',3)   -- author
ON CONFLICT (class_id, property_template_id) DO NOTHING;
