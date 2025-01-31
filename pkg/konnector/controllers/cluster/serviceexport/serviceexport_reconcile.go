/*
Copyright 2022 The Kube Bind Authors.

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

package serviceexport

import (
	"context"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	kubebindv1alpha1 "github.com/kube-bind/kube-bind/pkg/apis/kubebind/v1alpha1"
	kubebindhelpers "github.com/kube-bind/kube-bind/pkg/apis/kubebind/v1alpha1/helpers"
	conditionsapi "github.com/kube-bind/kube-bind/pkg/apis/third_party/conditions/apis/conditions/v1alpha1"
	"github.com/kube-bind/kube-bind/pkg/apis/third_party/conditions/util/conditions"
)

type reconciler struct {
	listServiceBinding       func(export string) ([]*kubebindv1alpha1.APIServiceBinding, error)
	getServiceExportResource func(name string) (*kubebindv1alpha1.APIServiceExportResource, error)
}

func (r *reconciler) reconcile(ctx context.Context, export *kubebindv1alpha1.APIServiceExport) error {
	var errs []error

	bindings, err := r.listServiceBinding(export.Name)
	if err != nil {
		return err
	}
	if len(bindings) == 0 {
		conditions.MarkFalse(
			export,
			kubebindv1alpha1.APIServiceExportConditionConnected,
			"NoServiceBinding",
			conditionsapi.ConditionSeverityInfo,
			"No ServiceBindings found for APIServiceExport",
		)
	} else if len(bindings) > 1 {
		conditions.MarkFalse(
			export,
			kubebindv1alpha1.APIServiceExportConditionConnected,
			"MultipleServiceBindings",
			conditionsapi.ConditionSeverityError,
			"Multiple ServiceBindings found for APIServiceExport. Delete all but one.",
		)
	} else {
		conditions.MarkTrue(
			export,
			kubebindv1alpha1.APIServiceExportConditionConnected,
		)

		if err := r.ensureServiceBindingConditionCopied(ctx, export, bindings[0]); err != nil {
			errs = append(errs, err)
		}
	}

	if err := r.ensureResourcesExist(ctx, export); err != nil {
		errs = append(errs, err)
	}

	conditions.SetSummary(export)

	return utilerrors.NewAggregate(errs)
}

func (r *reconciler) ensureServiceBindingConditionCopied(ctx context.Context, export *kubebindv1alpha1.APIServiceExport, binding *kubebindv1alpha1.APIServiceBinding) error {
	if inSync := conditions.Get(binding, kubebindv1alpha1.APIServiceBindingConditionSchemaInSync); inSync != nil {
		conditions.Set(export, inSync)
	} else {
		conditions.MarkFalse(
			export,
			kubebindv1alpha1.APIServiceExportConditionSchemaInSync,
			"Unknown",
			conditionsapi.ConditionSeverityInfo,
			"APIServiceBinding %s in the consumer cluster does not have a SchemaInSync condition.",
			binding.Name,
		)
	}

	if ready := conditions.Get(binding, conditionsapi.ReadyCondition); ready != nil {
		clone := *ready
		clone.Type = kubebindv1alpha1.APIServiceExportConditionServiceBindingReady
		conditions.Set(export, &clone)
	} else {
		conditions.MarkFalse(
			export,
			kubebindv1alpha1.APIServiceExportConditionServiceBindingReady,
			"Unknown",
			conditionsapi.ConditionSeverityInfo,
			"SerciceBinding %s in the consumer cluster does not have a Ready condition.",
			binding.Name,
		)
	}

	return nil
}

func (r *reconciler) ensureResourcesExist(ctx context.Context, export *kubebindv1alpha1.APIServiceExport) error {
	var errs []error

	resourceValid := true
	for _, resource := range export.Spec.Resources {
		name := resource.Resource + "." + resource.Group
		resource, err := r.getServiceExportResource(name)
		if err != nil && !errors.IsNotFound(err) {
			errs = append(errs, err)
			continue
		} else if errors.IsNotFound(err) {
			conditions.MarkFalse(
				export,
				kubebindv1alpha1.APIServiceExportConditionResourcesValid,
				"ServiceExportResourceNotFound",
				conditionsapi.ConditionSeverityError,
				"APIServiceExportResource %s not found on the service provider cluster.",
				name,
			)
			resourceValid = false
			continue
		}

		if resource.Spec.Scope != apiextensionsv1.NamespaceScoped && export.Spec.Scope != kubebindv1alpha1.ClusterScope {
			conditions.MarkFalse(
				export,
				kubebindv1alpha1.APIServiceExportConditionResourcesValid,
				"ServiceExportResourceWrongScope",
				conditionsapi.ConditionSeverityError,
				"APIServiceExportResource %s is Cluster scope, but the APIServiceExport is not.",
				name,
			)
			resourceValid = false
			continue
		}

		if _, err := kubebindhelpers.ServiceExportResourceToCRD(resource); err != nil {
			conditions.MarkFalse(
				export,
				kubebindv1alpha1.APIServiceExportConditionResourcesValid,
				"ServiceExportResourceInvalid",
				conditionsapi.ConditionSeverityError,
				"APIServiceExportResource %s on the service provider cluster is invalid: %s",
				name, err,
			)
			resourceValid = false
			continue
		}
	}

	if resourceValid {
		conditions.MarkTrue(
			export,
			kubebindv1alpha1.APIServiceExportConditionResourcesValid,
		)
	}

	return utilerrors.NewAggregate(errs)
}
