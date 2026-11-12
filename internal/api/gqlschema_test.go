package api

import (
	"testing"

	graphql "github.com/graph-gophers/graphql-go"
)

func TestStorefrontSchemaParses(t *testing.T) {
	if _, err := graphql.ParseSchema(storefrontSchema, &gqlRoot{}, graphql.MaxDepth(7), graphql.UseFieldResolvers()); err != nil {
		t.Fatal(err)
	}
}
