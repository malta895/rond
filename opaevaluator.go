package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/open-policy-agent/opa/ast"
	"github.com/open-policy-agent/opa/rego"
)

type Evaluator interface {
	Eval(ctx context.Context) (rego.ResultSet, error)
	Partial(ctx context.Context) (*rego.PartialQueries, error)
}

var unknowns = []string{"data.resources"}

type OPAEvaluator struct {
	PermissionQuery         Evaluator
	RequiredAllowPermission string
}

func NewOPAEvaluator(policy string, opaModuleConfig *OPAModuleConfig, input map[string]interface{}) (*OPAEvaluator, error) {
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed input JSON encode: %v", err)
	}

	inputTerm, err := ast.ParseTerm(string(inputBytes))
	if err != nil {
		return nil, fmt.Errorf("failed input parse: %v", err)
	}

	sanitizedPolicy := strings.Replace(policy, ".", "_", -1)
	queryString := fmt.Sprintf("data.policies.%s", sanitizedPolicy)
	query := rego.New(
		rego.Query(queryString),
		rego.Module(opaModuleConfig.Name, opaModuleConfig.Content),
		rego.ParsedInput(inputTerm.Value),
		rego.Unknowns(unknowns),
		getHeaderFunction,
	)

	return &OPAEvaluator{
		PermissionQuery:         query,
		RequiredAllowPermission: policy,
	}, nil
}
