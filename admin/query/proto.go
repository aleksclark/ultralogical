package query

import (
	adminv1 "github.com/aleksclark/ultracore/gen/go/admin/v1"
)

// SearchFromProto converts the shared admin SearchRequest into a query.Request.
func SearchFromProto(s *adminv1.SearchRequest) Request {
	if s == nil {
		return Request{}
	}
	req := Request{Query: s.GetQuery()}
	if p := s.GetPage(); p != nil {
		req.Page = Page{Limit: p.GetLimit(), Cursor: p.GetCursor()}
	}
	for _, f := range s.GetFilters() {
		req.Filters = append(req.Filters, Filter{
			Field:  f.GetField(),
			Op:     Op(f.GetOp()),
			Value:  f.GetValue(),
			Values: append([]string(nil), f.GetValues()...),
		})
	}
	for _, sort := range s.GetSorts() {
		req.Sorts = append(req.Sorts, Sort{Field: sort.GetField(), Descending: sort.GetDescending()})
	}
	return req
}

// PageInfoToProto converts PageInfo.
func PageInfoToProto(p PageInfo) *adminv1.PageInfo {
	return &adminv1.PageInfo{NextCursor: p.NextCursor, HasMore: p.HasMore}
}

// CollectionToProto converts a descriptor for DescribeCollection.
func CollectionToProto(c Collection) *adminv1.CollectionDescriptor {
	out := &adminv1.CollectionDescriptor{
		Name:        c.Name,
		Description: c.Description,
		HasDetail:   c.HasDetail,
		HasBlob:     c.HasBlob,
	}
	for _, f := range c.Fields {
		if f.PK && out.PrimaryKey == "" {
			out.PrimaryKey = f.Name
		} else if f.PK {
			out.PrimaryKey += "," + f.Name
		}
		fd := &adminv1.FieldDescriptor{
			Name:        f.Name,
			Type:        fieldTypeToProto(f.Type),
			Sortable:    f.Sortable,
			Searchable:  f.Searchable,
			Description: f.Description,
		}
		for _, op := range f.FilterOps {
			fd.FilterOps = append(fd.FilterOps, opToProto(op))
		}
		out.Fields = append(out.Fields, fd)
	}
	for _, s := range c.DefaultSorts {
		out.DefaultSorts = append(out.DefaultSorts, &adminv1.Sort{
			Field: s.Field, Descending: s.Descending,
		})
	}
	return out
}

func fieldTypeToProto(t FieldType) adminv1.FieldType {
	switch t {
	case TypeString:
		return adminv1.FieldType_FIELD_TYPE_STRING
	case TypeUUID:
		return adminv1.FieldType_FIELD_TYPE_UUID
	case TypeInt:
		return adminv1.FieldType_FIELD_TYPE_INT
	case TypeBool:
		return adminv1.FieldType_FIELD_TYPE_BOOL
	case TypeTimestamp:
		return adminv1.FieldType_FIELD_TYPE_TIMESTAMP
	case TypeJSON:
		return adminv1.FieldType_FIELD_TYPE_JSON
	default:
		return adminv1.FieldType_FIELD_TYPE_UNSPECIFIED
	}
}

func opToProto(op Op) adminv1.FilterOp {
	switch op {
	case OpEq:
		return adminv1.FilterOp_FILTER_OP_EQ
	case OpNe:
		return adminv1.FilterOp_FILTER_OP_NE
	case OpLt:
		return adminv1.FilterOp_FILTER_OP_LT
	case OpLte:
		return adminv1.FilterOp_FILTER_OP_LTE
	case OpGt:
		return adminv1.FilterOp_FILTER_OP_GT
	case OpGte:
		return adminv1.FilterOp_FILTER_OP_GTE
	case OpIn:
		return adminv1.FilterOp_FILTER_OP_IN
	case OpNotIn:
		return adminv1.FilterOp_FILTER_OP_NOT_IN
	case OpContains:
		return adminv1.FilterOp_FILTER_OP_CONTAINS
	case OpPrefix:
		return adminv1.FilterOp_FILTER_OP_PREFIX
	case OpIsNull:
		return adminv1.FilterOp_FILTER_OP_IS_NULL
	case OpIsNotNull:
		return adminv1.FilterOp_FILTER_OP_IS_NOT_NULL
	default:
		return adminv1.FilterOp_FILTER_OP_UNSPECIFIED
	}
}
