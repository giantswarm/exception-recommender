package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ErrorOp  = "error"
	UpdateOp = "updated"
	NoOp     = "unchanged"
	CreateOp = "created"
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
		// Create a deep copy of the existing object before the patch operation
		existingBeforePatch := existingObj.DeepCopy()

		// Update:
		obj.SetResourceVersion(existingObj.GetResourceVersion())
		obj.SetUID(existingObj.GetUID())

		err = r.Patch(ctx, obj, client.MergeFrom(existingObj.DeepCopy()))
		if err != nil {
			return ErrorOp, err
		}

		// Fetch the object after the patch operation
		err = r.Get(ctx, client.ObjectKeyFromObject(obj), &existingObj)
		if err != nil {
			return ErrorOp, err
		}

		// Compare the object before and after the patch operation
		if equality.Semantic.DeepEqual(existingBeforePatch.Object, existingObj.Object) {
			return NoOp, nil
		} else {
			return UpdateOp, nil
		}
	case errors.IsNotFound(err):
		// Create:
		err = r.Create(ctx, obj)
		if err != nil {
			return ErrorOp, err
		}
		return CreateOp, err
	default:
		return ErrorOp, err
	}
}
