package scaffold

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/c4factory/internal/model"
)

// defaultImage is used when the Framework brings no devcontainer settings.
const defaultImage = "mcr.microsoft.com/devcontainers/base:bookworm"

// assembleDevcontainer writes .devcontainer/: the Framework's base settings plus
// one compose file per Connector. Without Connectors the devcontainer is
// image-based; with them it switches to Docker Compose, with the application in
// a service called "app" that Connector fragments can extend.
func assembleDevcontainer(in Input, layers []Layer, opts Options) (Tree, error) {
	out := Tree{}
	settings := map[string]any{"image": defaultImage}
	var composeFiles []string

	for _, layer := range layers {
		switch layer.Side {
		case model.SideFramework:
			data, ok, err := metaFile(in, layer, "devcontainer.json")
			if err != nil {
				return nil, err
			}
			if ok {
				settings = map[string]any{}
				if err := json.Unmarshal(data, &settings); err != nil {
					return nil, &TemplateError{Layer: layer.Name, Path: metaDir + "devcontainer.json", Err: err}
				}
				if settings == nil { // the file was null
					return nil, &TemplateError{Layer: layer.Name, Path: metaDir + "devcontainer.json",
						Err: errors.New("want a JSON object")}
				}
			}
		case model.SideConnector:
			data, ok, err := metaFile(in, layer, "compose.yaml")
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			var fragment map[string]any
			if err := yaml.Unmarshal(data, &fragment); err != nil {
				return nil, &TemplateError{Layer: layer.Name, Path: metaDir + "compose.yaml", Err: err}
			}
			name := "compose." + layer.Name + ".yaml"
			composeFiles = append(composeFiles, name)
			out[devcontainerDir+name] = File{Data: data}
		}
	}

	settings["name"] = displayName(in)
	features := map[string]any{}
	if base, ok := settings["features"].(map[string]any); ok {
		maps.Copy(features, base)
	}
	maps.Copy(features, opts.Features)
	if len(features) > 0 {
		settings["features"] = features
	}

	if len(composeFiles) > 0 {
		image, ok := settings["image"].(string)
		if !ok || image == "" {
			return nil, fmt.Errorf("the Framework's devcontainer settings need an image when Connectors are used")
		}
		delete(settings, "image")
		settings["dockerComposeFile"] = append([]string{"compose.yaml"}, composeFiles...)
		settings["service"] = "app"
		settings["workspaceFolder"] = "/workspaces/${localWorkspaceFolderBasename}"
		settings["shutdownAction"] = "stopCompose"
		out[devcontainerDir+"compose.yaml"] = File{Data: []byte(appCompose(image))}
	}

	// People read and edit this file, so keep "&&" as it is rather than \u0026.
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil {
		return nil, err
	}
	out[devcontainerDir+"devcontainer.json"] = File{Data: data.Bytes()}

	for _, p := range opts.DevcontainerFiles.Paths() {
		target := devcontainerDir + p
		if _, taken := out[target]; taken {
			return nil, &ConflictError{Path: target, Owners: [2]string{operatorOwner, operatorOwner}}
		}
		out[target] = opts.DevcontainerFiles[p]
	}
	return out, nil
}

// appCommand keeps the app container up. Stopping a devcontainer only signals
// its first process, so this one passes SIGTERM on to the Session client and gives
// it a few seconds to delete its Session, rather than leaving it to turn Stale.
const appCommand = `    # Keeps the container up. When the devcontainer stops, it stops the Session
    # client first, so the client deletes its Session instead of leaving it Stale.
    command:
      - /bin/sh
      - -c
      - >-
        trap 'pkill -TERM -x c4session; for i in 1 2 3 4 5 6 7 8 9 10; do pkill -0 -x c4session || break; sleep 0.5; done; exit 0' TERM INT;
        sleep infinity & wait
`

func appCompose(image string) string {
	var b strings.Builder
	b.WriteString("# Written by C4 Factory. The application's devcontainer; each\n")
	b.WriteString("# compose.<connector>.yaml next to this file adds a service it talks to.\n")
	b.WriteString("services:\n")
	b.WriteString("  app:\n")
	fmt.Fprintf(&b, "    image: %s\n", image)
	b.WriteString(appCommand)
	b.WriteString("    volumes:\n")
	b.WriteString("      - ../..:/workspaces:cached\n")
	return b.String()
}

func displayName(in Input) string {
	if in.DisplayName != "" {
		return in.DisplayName
	}
	return in.Container
}
