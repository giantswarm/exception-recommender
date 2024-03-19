package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Controller struct {
	client.Client
}

// CreateOrUpdate attempts first to patch the object given but if an IsNotFound error
// is returned it instead creates the resource.
func (r *Controller) CreateOrUpdate(ctx context.Context, obj client.Object) (string, error) {
	existingObj := unstructured.Unstructured{}
	existingObj.SetGroupVersionKind(obj.GetObjectKind().GroupVersionKind())

	err := r.Get(ctx, client.ObjectKeyFromObject(obj), &existingObj)
	switch {
	case err == nil:
		// Update:
		obj.SetResourceVersion(existingObj.GetResourceVersion())
		obj.SetUID(existingObj.GetUID())
		err = r.Patch(ctx, obj, client.MergeFrom(existingObj.DeepCopy()))
		return "update", err
	case errors.IsNotFound(err):
		// Create:
		err = r.Create(ctx, obj)
		return "create", err
	default:
		return "untouched", err
	}
}
