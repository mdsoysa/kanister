// Copyright 2019 The Kanister Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package function

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig"
	kanister "github.com/kanisterio/kanister/pkg"
	crv1alpha1 "github.com/kanisterio/kanister/pkg/apis/cr/v1alpha1"
	"github.com/kanisterio/kanister/pkg/function/wait"
	"github.com/kanisterio/kanister/pkg/kube"
	"github.com/kanisterio/kanister/pkg/param"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type WaitConditions struct {
	AnyOf []Condition
	AllOf []Condition
}

type Condition struct {
	ObjectReference crv1alpha1.ObjectReference
	Condition       string
}

func init() {
	_ = kanister.Register(&waitFunc{})
}

var _ kanister.Func = (*waitFunc)(nil)

type waitFunc struct{}

func (*waitFunc) Name() string {
	return wait.FuncName
}

func (ktf *waitFunc) Exec(ctx context.Context, tp param.TemplateParams, args map[string]interface{}) (map[string]interface{}, error) {
	var timeout string
	var conditions WaitConditions
	var err error
	if err = Arg(args, wait.TimeoutArg, &timeout); err != nil {
		return nil, err
	}
	if err = Arg(args, wait.ConditionsArg, &conditions); err != nil {
		return nil, err
	}
	dynCli, err := kube.NewDynamicClient()
	if err != nil {
		return nil, err
	}
	//fmt.Printf("%#v\n", conditions)
	err = waitForCondition(dynCli, conditions)
	return nil, err
}

func (*waitFunc) RequiredArgs() []string {
	return []string{wait.TimeoutArg, wait.ConditionsArg}
}

func waitForCondition(dynCli dynamic.Interface, waitCond WaitConditions) error {
	// TODO: Use polling mechanism
	for _, cond := range waitCond.AnyOf {
		result, err := evaluateCondition(dynCli, cond)
		if err != nil {
			return err
		}
		if result {
			return nil
		}
	}
	return nil
}

func evaluateCondition(dynCli dynamic.Interface, cond Condition) (bool, error) {
	obj, err := fetchObjectFromRef(dynCli, cond.ObjectReference)
	if err != nil {
		return false, err
	}
	rcondition, err := resolveJsonpath(obj, cond.Condition)
	if err != nil {
		return false, err
	}
	fmt.Println("Resolved conditions", rcondition, err)
	t, err := template.New("config").Option("missingkey=error").Funcs(sprig.TxtFuncMap()).Parse(rcondition)
	if err != nil {
		return false, errors.WithStack(err)
	}
	buf := bytes.NewBuffer(nil)
	if err = t.Execute(buf, nil); err != nil {
		return false, errors.WithStack(err)
	}
	return buf.String() == "true", nil
}

func fetchObjectFromRef(dynCli dynamic.Interface, objRef crv1alpha1.ObjectReference) (runtime.Object, error) {
	gvr := schema.GroupVersionResource{Group: objRef.Group, Version: objRef.APIVersion, Resource: objRef.Resource}
	return dynCli.Resource(gvr).Get(context.TODO(), objRef.Name, metav1.GetOptions{})
}

func resolveJsonpath(obj runtime.Object, condStr string) (string, error) {
	resolvedCondStr := condStr
	slist := strings.Fields(condStr)
	for _, s := range slist {
		if strings.HasPrefix(s, "$.") {
			value, err := kube.ResolveJsonpathToString(obj, fmt.Sprintf("{%s}", strings.TrimPrefix(s, "$")))
			if err != nil {
				return resolvedCondStr, err
			}
			// TODO: Check value type and don't add quotes if not string
			resolvedCondStr = strings.ReplaceAll(resolvedCondStr, s, fmt.Sprintf(`"%s"`, value))
		}
	}
	return resolvedCondStr, nil
}
