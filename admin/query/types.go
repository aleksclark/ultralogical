// Package query implements composable, allowlisted admin search primitives:
// collection descriptors, typed filters/sorts, signed keyset cursors, and
// SQL compilation that rejects invalid fields before any query reaches Postgres.
package query

import (
	"errors"
	"fmt"
	"time"
)

// Cost limits applied to every compiled search.
const (
	DefaultLimit        = 50
	MaxLimit            = 250
	MaxFilters          = 16
	MaxSorts            = 4
	MaxQueryLength      = 256
	MaxFilterValues     = 64
	DefaultCursorTTL    = 24 * time.Hour
	DefaultQueryTimeout = 5 * time.Second
	MaxPreviewBytes     = 256
	MaxSummaryBytes     = 64 * 1024
)

// FieldType is the scalar type of a descriptor field.
type FieldType string

const (
	TypeString    FieldType = "string"
	TypeUUID      FieldType = "uuid"
	TypeInt       FieldType = "int"
	TypeBool      FieldType = "bool"
	TypeTimestamp FieldType = "timestamp"
	TypeJSON      FieldType = "json"
)

// Op is a filter operator.
type Op string

const (
	OpEq        Op = "eq"
	OpNe        Op = "ne"
	OpLt        Op = "lt"
	OpLte       Op = "lte"
	OpGt        Op = "gt"
	OpGte       Op = "gte"
	OpIn        Op = "in"
	OpNotIn     Op = "not_in"
	OpContains  Op = "contains"
	OpPrefix    Op = "prefix"
	OpIsNull    Op = "is_null"
	OpIsNotNull Op = "is_not_null"
)

// Field describes one filterable/sortable column projection.
type Field struct {
	Name        string
	Column      string // SQL expression or qualified column
	Type        FieldType
	FilterOps   []Op
	Sortable    bool
	Searchable  bool
	Description string
	// PK marks primary-key components used for keyset tie-break.
	PK bool
	// CursorOmit excludes the field from cursor payloads even if sorted.
	CursorOmit bool
}

// Collection is the descriptor for one admin list surface.
type Collection struct {
	Name        string
	Description string
	From        string // FROM clause body, e.g. "tenants t"
	// Select is the SELECT list for summary rows (without SELECT keyword).
	Select string
	// SelectArgs are fixed args prepended before filter args.
	SelectArgs []any
	Fields     []Field
	// DefaultSorts are applied when the request omits sorts.
	DefaultSorts []Sort
	// SearchExpr is optional SQL boolean using $N placeholders starting after
	// fixed SelectArgs; the engine appends one arg (the query text) when used.
	// Example: "(t.name ILIKE '%' || $N || '%')" — use "?" as the placeholder
	// marker and the compiler substitutes the next arg index.
	SearchExpr string
	HasDetail  bool
	HasBlob    bool
}

// Filter is a typed predicate.
type Filter struct {
	Field  string
	Op     Op
	Value  string
	Values []string
}

// Sort is one order key.
type Sort struct {
	Field      string
	Descending bool
}

// Page is pagination input.
type Page struct {
	Limit  uint32
	Cursor string
}

// Request is the compiled-input envelope.
type Request struct {
	Query   string
	Filters []Filter
	Sorts   []Sort
	Page    Page
}

// PageInfo is pagination output.
type PageInfo struct {
	NextCursor string
	HasMore    bool
}

// Errors returned before SQL construction.
var (
	ErrInvalidLimit      = errors.New("query: limit exceeds maximum of 250")
	ErrInvalidField      = errors.New("query: unknown field")
	ErrInvalidOp         = errors.New("query: operator not allowed for field")
	ErrInvalidSort       = errors.New("query: field is not sortable")
	ErrTooManyFilters    = errors.New("query: too many filters")
	ErrTooManySorts      = errors.New("query: too many sorts")
	ErrQueryTooLong      = errors.New("query: text search too long")
	ErrBadCursor         = errors.New("query: invalid or expired cursor")
	ErrCursorMismatch    = errors.New("query: cursor does not match request")
	ErrUnknownCollection = errors.New("query: unknown collection")
	ErrBadValue          = errors.New("query: invalid filter value")
)

// ValidationError carries a human-readable reason.
type ValidationError struct {
	Err error
	Msg string
}

func (e *ValidationError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return e.Err.Error()
}

func (e *ValidationError) Unwrap() error { return e.Err }

func invalid(err error, format string, args ...any) error {
	return &ValidationError{Err: err, Msg: fmt.Sprintf(format, args...)}
}
