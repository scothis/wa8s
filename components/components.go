/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package components

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	extism "github.com/extism/go-sdk"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/samyfodil/wazy"
	"github.com/samyfodil/wazy/component"
	"github.com/tetratelabs/wazero"

	componentsv1alpha1 "reconciler.io/wa8s/apis/components/v1alpha1"
)

var runtime wazy.Runtime
var compileCache *component.CompileCache

func init() {
	runtime = wazy.NewRuntime(context.Background())
	compileCache = component.NewCompileCache()
}

//go:embed wit-tools.wasm
var witToolsWasm []byte
var witToolsPool = bootstrapPool(witToolsWasm, "wit-tools.wasm")

func ExtractWIT(ctx context.Context, component []byte) (_ string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic calling ExtractWIT: %s", r)
		}
	}()

	plugin := witToolsPool.Get().(*extism.Plugin)
	defer witToolsPool.Put(plugin)

	_, out, err := plugin.CallWithContext(ctx, "extract", component)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

//go:embed static-config.wasm
var staticConfigWasm []byte

func ComponentizeConfigStore(ctx context.Context, config map[string]string) (_ []byte, err error) {
	inst, err := component.Instantiate(ctx, runtime, staticConfigWasm, component.WithCompileCache(compileCache))
	if err != nil {
		panic(fmt.Errorf("instantiate static-config: %s", err))
	}
	defer func() {
		err = inst.Close(ctx)
	}()

	values := []component.Value{}
	for key, value := range config {
		values = append(values, []component.Value{key, value})
	}

	got, err := inst.CallExport(ctx, "componentized:config/factory", "build-component", values)
	if err != nil {
		panic(fmt.Errorf("call static-config: %w", err))
	}
	result := got[0].(component.ResultValue)
	if result.IsErr {
		return nil, fmt.Errorf("call static-config: %s", result.Payload)
	}
	return result.Payload.([]byte), nil
}

//go:embed wac.wasm
var wacWasm []byte
var wacPool = bootstrapPool(wacWasm, "wac.wasm")

type ResolvedComponent struct {
	Name      string
	Image     name.Digest
	Component []byte
	WIT       componentsv1alpha1.WIT
}

func WACCompose(ctx context.Context, wac string, dependencies []ResolvedComponent) (_ []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic calling WACCompose: %s", r)
		}
	}()

	plugin := wacPool.Get().(*extism.Plugin)
	defer wacPool.Put(plugin)

	type WACDependency struct {
		Name      string `json:"name"`
		Component []byte `json:"component"`
	}
	type WAC struct {
		Script       string          `json:"script"`
		Dependencies []WACDependency `json:"dependencies"`
	}

	input := WAC{
		Script:       wac,
		Dependencies: []WACDependency{},
	}
	for _, dependency := range dependencies {
		input.Dependencies = append(input.Dependencies, WACDependency{
			Name:      dependency.Name,
			Component: dependency.Component,
		})
	}

	inputJson, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	_, component, err := plugin.CallWithContext(ctx, "compose", inputJson)
	if err != nil {
		return nil, err
	}

	return component, nil
}

func WACPlug(ctx context.Context, dependencies []ResolvedComponent) (_ []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic calling WACPlug: %s", r)
		}
	}()

	plugin := wacPool.Get().(*extism.Plugin)
	defer wacPool.Put(plugin)

	type WACDependency struct {
		Name      string `json:"name"`
		Component []byte `json:"component"`
	}
	type WAC struct {
		Script       string          `json:"script"`
		Dependencies []WACDependency `json:"dependencies"`
	}

	input := WAC{
		Script:       "",
		Dependencies: []WACDependency{},
	}
	for _, dependency := range dependencies {
		input.Dependencies = append(input.Dependencies, WACDependency{
			Name:      dependency.Name,
			Component: dependency.Component,
		})
	}

	inputJson, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	_, component, err := plugin.CallWithContext(ctx, "plug", inputJson)
	if err != nil {
		return nil, err
	}

	return component, nil
}

func bootstrapPool(wasm []byte, name string) sync.Pool {
	return sync.Pool{
		New: func() any {
			plugin, err := bootstrapPlugin(wasm, name)
			if err != nil {
				panic(err)
			}
			return plugin
		},
	}
}

func bootstrapPlugin(wasm []byte, name string) (*extism.Plugin, error) {
	manifest := extism.Manifest{
		Wasm: []extism.Wasm{
			extism.WasmData{
				Data: wasm,
				Name: name,
			},
		},
	}

	config := extism.PluginConfig{
		// EnableWasi:    true,
		RuntimeConfig: wazero.NewRuntimeConfig().WithCloseOnContextDone(true),
	}
	plugin, err := extism.NewPlugin(context.Background(), manifest, config, []extism.HostFunction{})
	if err != nil {
		return nil, err
	}
	return plugin, nil
}
