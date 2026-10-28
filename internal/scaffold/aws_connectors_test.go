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

// TestRDSConnectorConnectsToAWSLambdaTS: the rds Connector writes its cluster
// file and aws-lambda-ts wires it, alone and beside dynamodb (#1923,
// docs/plans/aws-rds.md).
func TestRDSConnectorConnectsToAWSLambdaTS(t *testing.T) {
	result := renderLambda(t, "rds")
	for _, path := range []string{"infra/connector_rds.ts", "infra/connector_rds.test.ts"} {
		if _, ok := result.Files[path]; !ok {
			t.Errorf("the rds Connector writes no %s", path)
		}
	}
	connectors := string(result.Files["infra/connectors.ts"].Data)
	for _, want := range []string{
		"import { connectRDS } from './connector_rds';",
		"= [['RDS', connectRDS]];",
	} {
		if !strings.Contains(connectors, want) {
			t.Errorf("infra/connectors.ts lacks %q:\n%s", want, connectors)
		}
	}
	if strings.Contains(connectors, "connector_dynamodb") {
		t.Errorf("infra/connectors.ts imports the dynamodb Connector it doesn't have:\n%s", connectors)
	}
	// The cluster follows the tier: the database is the Container's, the
	// Data API and IAM authentication are on, and what the tier keeps is
	// protected and snapshotted.
	cluster := string(result.Files["infra/connector_rds.ts"].Data)
	for _, want := range []string{
		"export const RDS_DATABASE_NAME = 'sample_app';",
		"defaultDatabaseName: RDS_DATABASE_NAME",
		"enableDataApi: true",
		"iamAuthentication: true",
		"deletionProtection: rules.keepData",
		"removalPolicy: rules.keepData ? RemovalPolicy.SNAPSHOT : RemovalPolicy.DESTROY",
		"cluster.grantDataApiAccess(user);",
	} {
		if !strings.Contains(cluster, want) {
			t.Errorf("infra/connector_rds.ts lacks %q", want)
		}
	}
	// The guard leaves nothing behind when the Framework wires it.
	if !strings.HasPrefix(cluster, "import { Duration, RemovalPolicy, Stack, Token, Validations } from 'aws-cdk-lib';\n") {
		t.Errorf("infra/connector_rds.ts doesn't start with its first import:\n%.120s", cluster)
	}

	both := renderLambda(t, "dynamodb", "rds")
	connectors = string(both.Files["infra/connectors.ts"].Data)
	for _, want := range []string{
		"import { connectDynamoDB } from './connector_dynamodb';",
		"import { connectRDS } from './connector_rds';",
		"  ['DynamoDB', connectDynamoDB],\n  ['RDS', connectRDS],\n];",
	} {
		if !strings.Contains(connectors, want) {
			t.Errorf("infra/connectors.ts with both Connectors lacks %q:\n%s", want, connectors)
		}
	}
	for _, path := range []string{"infra/connector_dynamodb.ts", "infra/connector_rds.ts"} {
		if _, ok := both.Files[path]; !ok {
			t.Errorf("both Connectors write no %s", path)
		}
	}
}

// TestRDSConnectorConnectsToAWSLambdaPyAndGo: aws-lambda-py and aws-lambda-go
// wire the rds Connector as aws-lambda-ts does, alone and beside dynamodb
// (#1924, docs/plans/aws-rds.md).
func TestRDSConnectorConnectsToAWSLambdaPyAndGo(t *testing.T) {
	for _, framework := range []string{"aws-lambda-py", "aws-lambda-go"} {
		t.Run(framework, func(t *testing.T) {
			result := renderAWS(t, model.TypeFunction, framework, "rds")
			for _, path := range []string{"infra/connector_rds.ts", "infra/connector_rds.test.ts"} {
				if _, ok := result.Files[path]; !ok {
					t.Errorf("the rds Connector writes no %s", path)
				}
			}
			connectors := string(result.Files["infra/connectors.ts"].Data)
			for _, want := range []string{
				"import { connectRDS } from './connector_rds';",
				"export type DataStore = Table | DatabaseCluster;",
				"= [['RDS', connectRDS]];",
			} {
				if !strings.Contains(connectors, want) {
					t.Errorf("infra/connectors.ts lacks %q:\n%s", want, connectors)
				}
			}
			if strings.Contains(connectors, "connector_dynamodb") {
				t.Errorf("infra/connectors.ts imports the dynamodb Connector it doesn't have:\n%s", connectors)
			}
			// The stack alarms on a cluster and backs it up by the ARN the Standard reads.
			stack := string(result.Files["infra/api_stack.ts"].Data)
			for _, want := range []string{
				"alarms.monitoring.monitorAuroraCluster({",
				"addMaxConnectionCountAlarm: { Critical: { maxConnectionCount: maxConnections(cluster) } },",
				"clusters: clusters.map(([, cluster]) => cluster),",
			} {
				if !strings.Contains(stack, want) {
					t.Errorf("infra/api_stack.ts lacks %q", want)
				}
			}
			if backups := string(result.Files["infra/backups.ts"].Data); !strings.Contains(backups, "(cluster.node.defaultChild as CfnDBCluster).attrDbClusterArn") {
				t.Error("infra/backups.ts doesn't select a cluster by its DBClusterArn")
			}
			sdk := map[string]string{"aws-lambda-py": "execute_statement", "aws-lambda-go": "ExecuteStatement"}[framework]
			readme := string(result.Files["README.md"].Data)
			for _, want := range []string{"RDS_CLUSTER_ARN", "RDS_SECRET_ARN", "RDS_DATABASE", sdk} {
				if !strings.Contains(readme, want) {
					t.Errorf("README.md lacks %q", want)
				}
			}

			both := renderAWS(t, model.TypeFunction, framework, "dynamodb", "rds")
			connectors = string(both.Files["infra/connectors.ts"].Data)
			for _, want := range []string{
				"import { connectDynamoDB } from './connector_dynamodb';",
				"import { connectRDS } from './connector_rds';",
				"  ['DynamoDB', connectDynamoDB],\n  ['RDS', connectRDS],\n];",
			} {
				if !strings.Contains(connectors, want) {
					t.Errorf("infra/connectors.ts with both Connectors lacks %q:\n%s", want, connectors)
				}
			}
			for _, path := range []string{"infra/connector_dynamodb.ts", "infra/connector_rds.ts"} {
				if _, ok := both.Files[path]; !ok {
					t.Errorf("both Connectors write no %s", path)
				}
			}
		})
	}
}

// TestRDSConnectorConnectsToAWSServices: aws-fargate-ts and aws-ec2-ts wire
// the rds Connector with their own VPC and security group, alone and beside
// dynamodb (#1926, docs/plans/aws-rds.md).
func TestRDSConnectorConnectsToAWSServices(t *testing.T) {
	for _, tc := range []struct{ framework, group string }{
		{"aws-fargate-ts", "serviceSecurityGroup"},
		{"aws-ec2-ts", "instanceSecurityGroup"},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			result := renderAWS(t, model.TypeService, tc.framework, "rds")
			for _, path := range []string{"infra/connector_rds.ts", "infra/connector_rds.test.ts"} {
				if _, ok := result.Files[path]; !ok {
					t.Errorf("the rds Connector writes no %s", path)
				}
			}
			connectors := string(result.Files["infra/connectors.ts"].Data)
			for _, want := range []string{
				"import { connectRDS } from './connector_rds';",
				"export type DataStore = Table | DatabaseCluster;",
				"= [['RDS', connectRDS]];",
			} {
				if !strings.Contains(connectors, want) {
					t.Errorf("infra/connectors.ts lacks %q:\n%s", want, connectors)
				}
			}
			if strings.Contains(connectors, "connector_dynamodb") {
				t.Errorf("infra/connectors.ts imports the dynamodb Connector it doesn't have:\n%s", connectors)
			}
			// The cluster joins the stack's VPC behind the user's security
			// group, is alarmed on and backed up by the ARN the Standard reads.
			stack := string(result.Files["infra/service_stack.ts"].Data)
			wants := []string{
				"network: { vpc, connections: " + tc.group + ".connections },",
				"alarms.monitoring.monitorAuroraCluster({",
				"addMaxConnectionCountAlarm: { Critical: { maxConnectionCount: maxConnections(cluster) } },",
				"clusters: clusters.map(([, cluster]) => cluster),",
			}
			if tc.framework == "aws-ec2-ts" {
				// Its VPC has no way out: the server and the rotation reach
				// Secrets Manager and the Data API through endpoints.
				wants = append(wants, "InterfaceVpcEndpointAwsService.SECRETS_MANAGER", "InterfaceVpcEndpointAwsService.RDS_DATA")
			}
			for _, want := range wants {
				if !strings.Contains(stack, want) {
					t.Errorf("infra/service_stack.ts lacks %q", want)
				}
			}
			if backups := string(result.Files["infra/backups.ts"].Data); !strings.Contains(backups, "(cluster.node.defaultChild as CfnDBCluster).attrDbClusterArn") {
				t.Error("infra/backups.ts doesn't select a cluster by its DBClusterArn")
			}
			readme := string(result.Files["README.md"].Data)
			for _, want := range []string{"RDS_CLUSTER_ARN", "RDS_SECRET_ARN", "RDS_DATABASE", "RDS_HOST", "RDS_PORT", "ExecuteStatementCommand"} {
				if !strings.Contains(readme, want) {
					t.Errorf("README.md lacks %q", want)
				}
			}

			both := renderAWS(t, model.TypeService, tc.framework, "dynamodb", "rds")
			connectors = string(both.Files["infra/connectors.ts"].Data)
			for _, want := range []string{
				"import { connectDynamoDB } from './connector_dynamodb';",
				"import { connectRDS } from './connector_rds';",
				"  ['DynamoDB', connectDynamoDB],\n  ['RDS', connectRDS],\n];",
			} {
				if !strings.Contains(connectors, want) {
					t.Errorf("infra/connectors.ts with both Connectors lacks %q:\n%s", want, connectors)
				}
			}
			for _, path := range []string{"infra/connector_dynamodb.ts", "infra/connector_rds.ts"} {
				if _, ok := both.Files[path]; !ok {
					t.Errorf("both Connectors write no %s", path)
				}
			}
			if readme := string(both.Files["README.md"].Data); !strings.Contains(readme, "DYNAMODB_TABLE_NAME") || !strings.Contains(readme, "RDS_HOST") {
				t.Error("README.md with both Connectors doesn't name both stores' variables")
			}
		})
	}
}

// TestRDSConnectorFailsAFrameworkThatDoesNotWireIt: as dynamodb's guard, the
// rds Connector's file refuses a Framework with no infra/connectors.ts (#1923).
func TestRDSConnectorFailsAFrameworkThatDoesNotWireIt(t *testing.T) {
	in, layers := loadCombo(t, model.TypeFunction, []side{
		{model.SideFramework, "aws-lambda-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideConnector, "rds"},
	}, "")
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
		t.Fatalf("Build with a Framework that doesn't wire rds: got %v, want a *TemplateError", err)
	}
	if tmplErr.Layer != "rds" || tmplErr.Path != "infra/connector_rds.ts.tmpl" {
		t.Errorf("TemplateError names layer %q path %q, want rds infra/connector_rds.ts.tmpl", tmplErr.Layer, tmplErr.Path)
	}
	for _, want := range []string{"aws-lambda-other", "doesn't wire the rds Connector", "infra/connectors.ts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
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

// aws-lambda-py and aws-lambda-go wire the Connector as aws-lambda-ts does
// (#1740): infra/connectors.ts lists it with it, and nothing without it.
func TestDynamoDBConnectorConnectsToOtherAWSLambdas(t *testing.T) {
	for _, framework := range []string{"aws-lambda-py", "aws-lambda-go"} {
		t.Run(framework, func(t *testing.T) {
			with := renderAWS(t, model.TypeFunction, framework, "dynamodb")
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

			without := renderAWS(t, model.TypeFunction, framework)
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

// aws-queue-ts, aws-event-ts (worker) and aws-schedule-ts (job) wire the
// Connector as aws-lambda-ts does (#1741): infra/connectors.ts lists it with
// it, and nothing without it.
func TestDynamoDBConnectorConnectsToAWSWorkersAndJobs(t *testing.T) {
	for _, tc := range []struct{ typ, framework string }{
		{model.TypeWorker, "aws-queue-ts"},
		{model.TypeWorker, "aws-event-ts"},
		{model.TypeJob, "aws-schedule-ts"},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			with := renderAWS(t, tc.typ, tc.framework, "dynamodb")
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

			without := renderAWS(t, tc.typ, tc.framework)
			if _, ok := without.Files["infra/connector_dynamodb.ts"]; ok {
				t.Errorf("%s alone writes infra/connector_dynamodb.ts", tc.framework)
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

// A worker Framework that renders no infra/connectors.ts (go-cli, a real one)
// fails with the Connector's own message, as a function one does (#1741).
func TestDynamoDBConnectorFailsAWorkerFrameworkThatDoesNotWireIt(t *testing.T) {
	in, layers := loadCombo(t, model.TypeWorker, []side{
		{model.SideFramework, "go-cli"},
		{model.SideStandard, "go-standard"},
		{model.SideConnector, "dynamodb"},
	}, "")
	_, err := Build(in, layers, Options{})
	var tmplErr *TemplateError
	if !errors.As(err, &tmplErr) {
		t.Fatalf("Build of a go-cli worker with dynamodb: got %v, want a *TemplateError", err)
	}
	if tmplErr.Layer != "dynamodb" || tmplErr.Path != "infra/connector_dynamodb.ts.tmpl" {
		t.Errorf("TemplateError names layer %q path %q, want dynamodb infra/connector_dynamodb.ts.tmpl", tmplErr.Layer, tmplErr.Path)
	}
	for _, want := range []string{"go-cli", "doesn't wire the dynamodb Connector", "infra/connectors.ts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}
