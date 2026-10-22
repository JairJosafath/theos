package scaffold

import (
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
	return renderAWSFunction(t, "aws-lambda-ts", connectors...)
}

// renderAWSFunction renders the function Framework with aws-standard and connectors.
func renderAWSFunction(t *testing.T, framework string, connectors ...string) Result {
	t.Helper()
	sides := make([]side, 0, 2+len(connectors))
	sides = append(sides,
		side{model.SideFramework, framework},
		side{model.SideStandard, "aws-standard"},
	)
	for _, c := range connectors {
		sides = append(sides, side{model.SideConnector, c})
	}
	in, layers := loadCombo(t, model.TypeFunction, sides, "")
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
		"table.grant(fn, ...DYNAMODB_ACTIONS)",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("infra/connector_dynamodb.ts lacks %q", want)
		}
	}
	if strings.Contains(table, "dynamodb:Scan") {
		t.Error("the dynamodb Connector grants Scan")
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

// aws-lambda-py and aws-lambda-go wire the Connector as aws-lambda-ts does
// (#1740): infra/connectors.ts lists it with it, and nothing without it.
func TestDynamoDBConnectorConnectsToOtherAWSLambdas(t *testing.T) {
	for _, framework := range []string{"aws-lambda-py", "aws-lambda-go"} {
		t.Run(framework, func(t *testing.T) {
			with := renderAWSFunction(t, framework, "dynamodb")
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

			without := renderAWSFunction(t, framework)
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
