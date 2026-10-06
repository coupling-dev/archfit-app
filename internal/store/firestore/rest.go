package firestore

import (
	"encoding/base64"
	"errors"
	"strconv"
)

// The subset of the Firestore REST API v1 the driver uses. Field names follow the proto3 JSON mapping.

type document struct {
	Name       string             `json:"name,omitempty"`
	Fields     map[string]fsValue `json:"fields,omitempty"`
	UpdateTime string             `json:"updateTime,omitempty"`
}

// fsValue is a Firestore Value. Exactly one member is set; pointers keep an empty value apart from a missing one.
type fsValue struct {
	IntegerValue *string `json:"integerValue,omitempty"` // int64 as a decimal string
	BytesValue   *string `json:"bytesValue,omitempty"`   // standard base64
}

func integerValue(n int64) fsValue {
	s := strconv.FormatInt(n, 10)
	return fsValue{IntegerValue: &s}
}

func bytesValue(b []byte) fsValue {
	s := base64.StdEncoding.EncodeToString(b)
	return fsValue{BytesValue: &s}
}

func (v fsValue) integer() (int64, error) {
	if v.IntegerValue == nil {
		return 0, errors.New("not an integer value")
	}
	return strconv.ParseInt(*v.IntegerValue, 10, 64)
}

type precond struct {
	Exists     *bool  `json:"exists,omitempty"`
	UpdateTime string `json:"updateTime,omitempty"`
}

type write struct {
	Update          *document `json:"update,omitempty"`
	Delete          string    `json:"delete,omitempty"`
	CurrentDocument *precond  `json:"currentDocument,omitempty"`
}

type commitRequest struct {
	Writes []write `json:"writes"`
}

type fieldRef struct {
	FieldPath string `json:"fieldPath"`
}

type projection struct {
	Fields []fieldRef `json:"fields"`
}

type collectionSelector struct {
	CollectionID string `json:"collectionId"`
}

type fieldFilter struct {
	Field fieldRef `json:"field"`
	Op    string   `json:"op"`
	Value fsValue  `json:"value"`
}

type filter struct {
	FieldFilter *fieldFilter `json:"fieldFilter,omitempty"`
}

type order struct {
	Field     fieldRef `json:"field"`
	Direction string   `json:"direction"`
}

type structuredQuery struct {
	Select  *projection          `json:"select,omitempty"`
	From    []collectionSelector `json:"from"`
	Where   *filter              `json:"where,omitempty"`
	OrderBy []order              `json:"orderBy,omitempty"`
	Limit   int                  `json:"limit,omitempty"`
}

type runQueryRequest struct {
	StructuredQuery structuredQuery `json:"structuredQuery"`
}

func tenantFilter(op string, t int64) *filter {
	return &filter{FieldFilter: &fieldFilter{Field: fieldRef{FieldPath: fieldTenant}, Op: op, Value: integerValue(t)}}
}
