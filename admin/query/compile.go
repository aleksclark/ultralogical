package query

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Compiled is a ready-to-run SQL query plan for one page.
type Compiled struct {
	// SQL is SELECT ... FROM ... WHERE ... ORDER BY ... LIMIT n
	SQL string
	// Args are bound parameters.
	Args []any
	// Limit is the requested page size (not limit+1).
	Limit int
	// Sorts is the effective sort list including PK tie-breakers.
	Sorts []Sort
	// SortColumns are SQL expressions matching Sorts order.
	SortColumns []string
	// Fingerprint binds cursors to this query shape.
	Fingerprint string
	// Cursor is the verified incoming cursor, if any.
	Cursor *CursorPayload
}

// Compiler validates requests against a collection and emits SQL.
type Compiler struct {
	Signer *Signer
}

// Compile validates req against col and produces a parameterized keyset query.
func (c *Compiler) Compile(col Collection, req Request) (*Compiled, error) {
	if err := validateRequest(col, req); err != nil {
		return nil, err
	}
	limit := int(req.Page.Limit)
	if limit == 0 {
		limit = DefaultLimit
	}

	fieldByName := map[string]Field{}
	for _, f := range col.Fields {
		fieldByName[f.Name] = f
	}

	sorts := req.Sorts
	if len(sorts) == 0 {
		sorts = append([]Sort(nil), col.DefaultSorts...)
	}
	// Always append PK fields as final stable tie-breakers.
	pkSorts := pkSorts(col)
	for _, pk := range pkSorts {
		if !containsSort(sorts, pk.Field) {
			sorts = append(sorts, pk)
		}
	}
	if len(sorts) == 0 {
		return nil, invalid(ErrInvalidSort, "collection %s has no sortable primary key", col.Name)
	}

	var sortCols []string
	for _, s := range sorts {
		f := fieldByName[s.Field]
		sortCols = append(sortCols, f.Column)
	}

	fp, err := fingerprint(col.Name, req, sorts)
	if err != nil {
		return nil, err
	}

	var cursor *CursorPayload
	if req.Page.Cursor != "" {
		if c.Signer == nil {
			return nil, fmt.Errorf("query: cursor provided but signer is nil")
		}
		p, err := c.Signer.Verify(req.Page.Cursor)
		if err != nil {
			return nil, err
		}
		if p.Collection != col.Name || p.Fingerprint != fp {
			return nil, invalid(ErrCursorMismatch, "cursor does not match this query")
		}
		if len(p.SortValues) != len(sorts) {
			return nil, invalid(ErrBadCursor, "cursor sort arity mismatch")
		}
		cursor = &p
	}

	args := append([]any(nil), col.SelectArgs...)
	var where []string

	// Text search.
	if q := strings.TrimSpace(req.Query); q != "" {
		if col.SearchExpr == "" {
			// Default: OR of searchable string fields with ILIKE.
			var parts []string
			for _, f := range col.Fields {
				if !f.Searchable || (f.Type != TypeString && f.Type != TypeJSON) {
					continue
				}
				args = append(args, "%"+escapeLike(q)+"%")
				parts = append(parts, fmt.Sprintf("(%s)::text ILIKE $%d ESCAPE '\\'", f.Column, len(args)))
			}
			if len(parts) == 0 {
				return nil, invalid(ErrInvalidField, "collection %s does not support text search", col.Name)
			}
			where = append(where, "("+strings.Join(parts, " OR ")+")")
		} else {
			args = append(args, q)
			expr := strings.ReplaceAll(col.SearchExpr, "?", fmt.Sprintf("$%d", len(args)))
			where = append(where, expr)
		}
	}

	// Filters.
	for _, fl := range req.Filters {
		f := fieldByName[fl.Field]
		clause, newArgs, err := compileFilter(f, fl, len(args)+1)
		if err != nil {
			return nil, err
		}
		where = append(where, clause)
		args = append(args, newArgs...)
	}

	// Keyset predicate from cursor.
	if cursor != nil {
		clause, newArgs, err := keysetPredicate(sorts, sortCols, cursor.SortValues, fieldByName, len(args)+1)
		if err != nil {
			return nil, err
		}
		where = append(where, clause)
		args = append(args, newArgs...)
	}

	var b strings.Builder
	b.WriteString("SELECT ")
	b.WriteString(col.Select)
	b.WriteString(" FROM ")
	b.WriteString(col.From)
	if len(where) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(where, " AND "))
	}
	b.WriteString(" ORDER BY ")
	var orderParts []string
	for i, s := range sorts {
		dir := "ASC"
		if s.Descending {
			dir = "DESC"
		}
		// NULLS LAST keeps keyset comparisons predictable.
		orderParts = append(orderParts, fmt.Sprintf("%s %s NULLS LAST", sortCols[i], dir))
	}
	b.WriteString(strings.Join(orderParts, ", "))
	// Fetch one extra row to detect has_more.
	args = append(args, limit+1)
	b.WriteString(fmt.Sprintf(" LIMIT $%d", len(args)))

	return &Compiled{
		SQL:         b.String(),
		Args:        args,
		Limit:       limit,
		Sorts:       sorts,
		SortColumns: sortCols,
		Fingerprint: fp,
		Cursor:      cursor,
	}, nil
}

// NextCursor builds a signed cursor from the last returned row's sort values.
func (c *Compiler) NextCursor(col Collection, compiled *Compiled, sortValues []string) (string, error) {
	if c.Signer == nil {
		return "", fmt.Errorf("query: cursor signer required")
	}
	return c.Signer.Sign(CursorPayload{
		Collection:  col.Name,
		Fingerprint: compiled.Fingerprint,
		SortValues:  sortValues,
	})
}

func validateRequest(col Collection, req Request) error {
	if req.Page.Limit > MaxLimit {
		return invalid(ErrInvalidLimit, "limit %d exceeds maximum %d", req.Page.Limit, MaxLimit)
	}
	if len(req.Filters) > MaxFilters {
		return invalid(ErrTooManyFilters, "at most %d filters allowed", MaxFilters)
	}
	if len(req.Sorts) > MaxSorts {
		return invalid(ErrTooManySorts, "at most %d sorts allowed", MaxSorts)
	}
	if len(req.Query) > MaxQueryLength {
		return invalid(ErrQueryTooLong, "query longer than %d characters", MaxQueryLength)
	}
	fieldByName := map[string]Field{}
	for _, f := range col.Fields {
		fieldByName[f.Name] = f
	}
	for _, fl := range req.Filters {
		f, ok := fieldByName[fl.Field]
		if !ok {
			return invalid(ErrInvalidField, "unknown filter field %q", fl.Field)
		}
		if !opAllowed(f, fl.Op) {
			return invalid(ErrInvalidOp, "operator %q not allowed on field %q", fl.Op, fl.Field)
		}
		if err := validateFilterValue(f, fl); err != nil {
			return err
		}
	}
	for _, s := range req.Sorts {
		f, ok := fieldByName[s.Field]
		if !ok {
			return invalid(ErrInvalidField, "unknown sort field %q", s.Field)
		}
		if !f.Sortable {
			return invalid(ErrInvalidSort, "field %q is not sortable", s.Field)
		}
	}
	return nil
}

func opAllowed(f Field, op Op) bool {
	for _, a := range f.FilterOps {
		if a == op {
			return true
		}
	}
	return false
}

func validateFilterValue(f Field, fl Filter) error {
	switch fl.Op {
	case OpIsNull, OpIsNotNull:
		return nil
	case OpIn, OpNotIn:
		if len(fl.Values) == 0 {
			return invalid(ErrBadValue, "field %q requires values for %s", f.Name, fl.Op)
		}
		if len(fl.Values) > MaxFilterValues {
			return invalid(ErrBadValue, "too many values for field %q", f.Name)
		}
		for _, v := range fl.Values {
			if err := parseTyped(f.Type, v); err != nil {
				return invalid(ErrBadValue, "field %q: %v", f.Name, err)
			}
		}
		return nil
	default:
		if fl.Value == "" && f.Type != TypeString {
			return invalid(ErrBadValue, "field %q requires a value", f.Name)
		}
		if err := parseTyped(f.Type, fl.Value); err != nil {
			return invalid(ErrBadValue, "field %q: %v", f.Name, err)
		}
		return nil
	}
}

func parseTyped(t FieldType, v string) error {
	switch t {
	case TypeString, TypeJSON:
		return nil
	case TypeUUID:
		if len(v) != 36 {
			return fmt.Errorf("expected uuid")
		}
		return nil
	case TypeInt:
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return fmt.Errorf("expected int")
		}
		return nil
	case TypeBool:
		if _, err := strconv.ParseBool(v); err != nil {
			return fmt.Errorf("expected bool")
		}
		return nil
	case TypeTimestamp:
		if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
			if _, err2 := time.Parse(time.RFC3339, v); err2 != nil {
				return fmt.Errorf("expected RFC3339 timestamp")
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported type %s", t)
	}
}

func coerce(t FieldType, v string) (any, error) {
	switch t {
	case TypeString, TypeUUID, TypeJSON:
		return v, nil
	case TypeInt:
		return strconv.ParseInt(v, 10, 64)
	case TypeBool:
		return strconv.ParseBool(v)
	case TypeTimestamp:
		if ts, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return ts.UTC(), nil
		}
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, err
		}
		return ts.UTC(), nil
	default:
		return v, nil
	}
}

func compileFilter(f Field, fl Filter, argStart int) (string, []any, error) {
	col := f.Column
	switch fl.Op {
	case OpIsNull:
		return col + " IS NULL", nil, nil
	case OpIsNotNull:
		return col + " IS NOT NULL", nil, nil
	case OpIn, OpNotIn:
		args := make([]any, 0, len(fl.Values))
		placeholders := make([]string, 0, len(fl.Values))
		for i, v := range fl.Values {
			cv, err := coerce(f.Type, v)
			if err != nil {
				return "", nil, invalid(ErrBadValue, "field %q: %v", f.Name, err)
			}
			args = append(args, cv)
			placeholders = append(placeholders, fmt.Sprintf("$%d", argStart+i))
		}
		op := "IN"
		if fl.Op == OpNotIn {
			op = "NOT IN"
		}
		return fmt.Sprintf("%s %s (%s)", col, op, strings.Join(placeholders, ", ")), args, nil
	case OpContains:
		cv := "%" + escapeLike(fl.Value) + "%"
		return fmt.Sprintf("%s ILIKE $%d ESCAPE '\\'", col, argStart), []any{cv}, nil
	case OpPrefix:
		cv := escapeLike(fl.Value) + "%"
		return fmt.Sprintf("%s ILIKE $%d ESCAPE '\\'", col, argStart), []any{cv}, nil
	default:
		sqlOp := map[Op]string{
			OpEq: "=", OpNe: "<>", OpLt: "<", OpLte: "<=", OpGt: ">", OpGte: ">=",
		}[fl.Op]
		if sqlOp == "" {
			return "", nil, invalid(ErrInvalidOp, "operator %q", fl.Op)
		}
		cv, err := coerce(f.Type, fl.Value)
		if err != nil {
			return "", nil, invalid(ErrBadValue, "field %q: %v", f.Name, err)
		}
		return fmt.Sprintf("%s %s $%d", col, sqlOp, argStart), []any{cv}, nil
	}
}

// keysetPredicate builds the row-value comparison for multi-column keyset
// pagination, respecting per-column ASC/DESC.
func keysetPredicate(sorts []Sort, cols []string, values []string, fields map[string]Field, argStart int) (string, []any, error) {
	// Expand (a,b,c) > (x,y,z) into:
	// (a > x) OR (a = x AND b > y) OR (a = x AND b = y AND c > z)
	// with direction flips for DESC.
	var parts []string
	var args []any
	for i := range sorts {
		var ands []string
		for j := 0; j < i; j++ {
			ands = append(ands, fmt.Sprintf("%s IS NOT DISTINCT FROM $%d", cols[j], argStart+len(args)))
			cv, err := coerce(fields[sorts[j].Field].Type, values[j])
			if err != nil {
				return "", nil, invalid(ErrBadCursor, "cursor value for %s: %v", sorts[j].Field, err)
			}
			args = append(args, cv)
		}
		cmp := ">"
		if sorts[i].Descending {
			cmp = "<"
		}
		// NULLS LAST: nulls never satisfy >/< against a non-null cursor value
		// in a useful way; treat IS NOT NULL and compare.
		ands = append(ands, fmt.Sprintf("(%s IS NOT NULL AND %s %s $%d)", cols[i], cols[i], cmp, argStart+len(args)))
		cv, err := coerce(fields[sorts[i].Field].Type, values[i])
		if err != nil {
			return "", nil, invalid(ErrBadCursor, "cursor value for %s: %v", sorts[i].Field, err)
		}
		args = append(args, cv)
		parts = append(parts, "("+strings.Join(ands, " AND ")+")")
	}
	return "(" + strings.Join(parts, " OR ") + ")", args, nil
}

func pkSorts(col Collection) []Sort {
	var out []Sort
	for _, f := range col.Fields {
		if f.PK && f.Sortable {
			out = append(out, Sort{Field: f.Name})
		}
	}
	return out
}

func containsSort(sorts []Sort, field string) bool {
	for _, s := range sorts {
		if s.Field == field {
			return true
		}
	}
	return false
}

func fingerprint(collection string, req Request, sorts []Sort) (string, error) {
	type fp struct {
		C string   `json:"c"`
		Q string   `json:"q"`
		F []Filter `json:"f"`
		S []Sort   `json:"s"`
	}
	raw, err := json.Marshal(fp{
		C: collection,
		Q: strings.TrimSpace(req.Query),
		F: req.Filters,
		S: sorts,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16]), nil
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// FormatSortValue converts a scanned sort value into a stable cursor string.
func FormatSortValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case *time.Time:
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339Nano)
	case [16]byte:
		// pgtype/pgx UUID
		return formatUUIDBytes(t[:])
	case []byte:
		if len(t) == 16 {
			return formatUUIDBytes(t)
		}
		return string(t)
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case int:
		return strconv.Itoa(t)
	case bool:
		return strconv.FormatBool(t)
	default:
		// fmt.Stringer (e.g. uuid.UUID)
		if s, ok := v.(interface{ String() string }); ok {
			return s.String()
		}
		return fmt.Sprint(t)
	}
}

func formatUUIDBytes(b []byte) string {
	if len(b) != 16 {
		return string(b)
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
