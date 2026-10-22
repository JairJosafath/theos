package scaffold

import (
	"errors"
	"strings"
	"testing"

	"github.com/JairJosafath/c4factory/internal/model"
)

// An AWS data store is a DataStore on a Connector: the dynamodb Connector
// writes the table, its grants and its backups into the Framework's CDK
// stack, through the Framework's infra/connectors.ts (#1117).

// renderLambda renders aws-lambda-ts with aws-standard and connectors.
func renderLambda(t *testing.T, connectors ...string) Result {
	t.Helper()
	return renderAWS(t, model.TypeFunction, "aws-lambda-ts", connectors...)
}

// renderAWS renders the Framework, as a Container of type typ, with
// aws-standard and connectors.
func renderAWS(t *testing.T, typ, framework string, connectors ...string) Result {
	t.Helper()
	sides := make([]side, 0, 2+len(connectors))
	sides = append(sides,
		side{model.SideFramework, framework},
		side{model.SideStandard, "aws-standard"},
	)
	for _, c := range connectors {
		sides = append(sides, side{model.SideConnector, c})
	}
	in, layers := loadCombo(t, typ, sides, "")
	result, err := Build(in, layers, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDynamoDBConnectorConnectsToAWSLambdaTS(t *testing.T) {
	result := renderLambda(t, "dynamodb")
	for _, path := range []string{"infra/connector_dynamodb.ts", "infra/connector_dynamodb.test.ts"} {
		if _, ok := result.Files[path]; !ok {
			t.Errorf("the dynamodb Connector writes no %s", path)
		}
	}
	connectors := string(result.Files["infra/connectors.ts"].Data)
	for _, want := range []string{
		"import { connectDynamoDB } from './connector_dynamodb';",
		"= [['DynamoDB', connectDynamoDB]];",
	} {
		if !strings.Contains(connectors, want) {
			t.Errorf("infra/connectors.ts lacks %q:\n%s", want, connectors)
		}
	}
	// The table follows the tier like the Framework's own: kept, protected
	// and recoverable in production, selected by the stack's backup plan.
	table := string(result.Files["infra/connector_dynamodb.ts"].Data)
	for _, want := range []string{
		"rules.pointInTimeRecovery",
		"deletionProtection: rules.keepData",
		"table.grant(user, ...DYNAMODB_ACTIONS)",
		"export type DataStoreUser = IGrantable & { addEnvironment(key: string, value: string): unknown };",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("infra/connector_dynamodb.ts lacks %q", want)
		}
	}
	if strings.Contains(table, "dynamodb:Scan") {
		t.Error("the dynamodb Connector grants Scan")
	}
	// The guard against a Framework that doesn't wire it leaves nothing
	// behind when the Framework does (#1739).
	if !strings.HasPrefix(table, "import { RemovalPolicy } from 'aws-cdk-lib';\n") {
		t.Errorf("infra/connector_dynamodb.ts doesn't start with its first import:\n%.120s", table)
	}
}

// TestDynamoDBConnectorFailsAFrameworkThatDoesNotWireIt: a function Framework
// without infra/connectors.ts never calls the Connector's file, so rendering
// it fails the Container with a TemplateError naming the Connector and the
// Framework, through the fail template function (#1739, decision 3 of
// docs/plans/aws-data-stores.md).
func TestDynamoDBConnectorFailsAFrameworkThatDoesNotWireIt(t *testing.T) {
	in, layers := loadCombo(t, model.TypeFunction, []side{
		{model.SideFramework, "aws-lambda-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideConnector, "dynamodb"},
	}, "")
	// A stand-in function Framework: a CDK app with no infra/connectors.ts.
	in.Framework = "aws-lambda-other"
	layers[0] = Layer{Side: model.SideFramework, Name: "aws-lambda-other", Ref: "local", Files: Tree{
		"README.md":           {Data: []byte("# {{ .DisplayName }}\n")},
		"infra/stack.ts":      {Data: []byte("export {};\n")},
		"src/handler.ts":      {Data: []byte("export const handler = async () => {};\n")},
		"src/handler.test.ts": {Data: []byte("export {};\n")},
	}}
	_, err := Build(in, layers, Options{})
	var tmplErr *TemplateError
	if !errors.As(err, &tmplErr) {
		t.Fatalf("Build with a Framework that doesn't wire dynamodb: got %v, want a *TemplateError", err)
	}
	if tmplErr.Layer != "dynamodb" || tmplErr.Path != "infra/connector_dynamodb.ts.tmpl" {
		t.Errorf("TemplateError names layer %q path %q, want dynamodb infra/connector_dynamodb.ts.tmpl", tmplErr.Layer, tmplErr.Path)
	}
	for _, want := range []string{"aws-lambda-other", "doesn't wire the dynamodb Connector", "infra/connectors.ts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestAWSLambdaTSWithoutConnectorsHasNoDataStores(t *testing.T) {
	result := renderLambda(t)
	if _, ok := result.Files["infra/connector_dynamodb.ts"]; ok {
		t.Error("aws-lambda-ts alone writes infra/connector_dynamodb.ts")
	}
	connectors := string(result.Files["infra/connectors.ts"].Data)
	if strings.Contains(connectors, "connector_dynamodb") {
		t.Errorf("infra/connectors.ts imports the dynamodb Connector it doesn't have:\n%s", connectors)
	}
	if !strings.Contains(connectors, "readonly (readonly [string, Connect])[] = [];") {
		t.Errorf("infra/connectors.ts has data stores without a Connector:\n%s", connectors)
	}
}

// aws-fargate-ts and aws-ec2-ts (service) wire the Connector as aws-lambda-ts
// does (#1742): infra/connectors.ts lists it with it, and nothing without it.
func TestDynamoDBConnectorConnectsToAWSServices(t *testing.T) {
	for _, framework := range []string{"aws-fargate-ts", "aws-ec2-ts"} {
		t.Run(framework, func(t *testing.T) {
			with := renderAWS(t, model.TypeService, framework, "dynamodb")
			if _, ok := with.Files["infra/connector_dynamodb.ts"]; !ok {
				t.Error("the dynamodb Connector writes no infra/connector_dynamodb.ts")
			}
			connectors := string(with.Files["infra/connectors.ts"].Data)
			for _, want := range []string{
				"import { connectDynamoDB } from './connector_dynamodb';",
				"= [['DynamoDB', connectDynamoDB]];",
			} {
				if !strings.Contains(connectors, want) {
					t.Errorf("infra/connectors.ts lacks %q:\n%s", want, connectors)
				}
			}
			if readme := string(with.Files["README.md"].Data); !strings.Contains(readme, "DYNAMODB_TABLE_NAME") {
				t.Error("README.md doesn't say new data goes in the DataStore's table")
			}

			without := renderAWS(t, model.TypeService, framework)
			if _, ok := without.Files["infra/connector_dynamodb.ts"]; ok {
				t.Errorf("%s alone writes infra/connector_dynamodb.ts", framework)
			}
			connectors = string(without.Files["infra/connectors.ts"].Data)
			if strings.Contains(connectors, "connector_dynamodb") ||
				!strings.Contains(connectors, "readonly (readonly [string, Connect])[] = [];") {
				t.Errorf("infra/connectors.ts has data stores without a Connector:\n%s", connectors)
			}
			if readme := string(without.Files["README.md"].Data); strings.Contains(readme, "DYNAMODB_TABLE_NAME") {
				t.Error("README.md names DYNAMODB_TABLE_NAME without the Connector")
			}
		})
	}
}

// A service Framework that renders no infra/connectors.ts (python-fastapi, a
// real one) fails with the Connector's own message, as a function one does
// (#1742).
func TestDynamoDBConnectorFailsAServiceFrameworkThatDoesNotWireIt(t *testing.T) {
	in, layers := loadCombo(t, model.TypeService, []side{
		{model.SideFramework, "python-fastapi"},
		{model.SideStandard, "python-standard"},
		{model.SideConnector, "dynamodb"},
	}, "")
	_, err := Build(in, layers, Options{})
	var tmplErr *TemplateError
	if !errors.As(err, &tmplErr) {
		t.Fatalf("Build of a python-fastapi service with dynamodb: got %v, want a *TemplateError", err)
	}
	if tmplErr.Layer != "dynamodb" || tmplErr.Path != "infra/connector_dynamodb.ts.tmpl" {
		t.Errorf("TemplateError names layer %q path %q, want dynamodb infra/connector_dynamodb.ts.tmpl", tmplErr.Layer, tmplErr.Path)
	}
	for _, want := range []string{"python-fastapi", "doesn't wire the dynamodb Connector", "infra/connectors.ts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}
