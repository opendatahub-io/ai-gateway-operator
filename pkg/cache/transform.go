package cache

import (
	"k8s.io/apimachinery/pkg/api/meta"
	toolscache "k8s.io/client-go/tools/cache"
)

const (
	// AnnotationLastAppliedConfiguration is the annotation added by kubectl apply
	// that is unnecessary when using server-side apply.
	AnnotationLastAppliedConfiguration = "kubectl.kubernetes.io/last-applied-configuration"
)

// StripUnusedFields returns a cache transform that removes managedFields
// and the last-applied-configuration annotation from cached objects.
// Both are unnecessary when using server-side apply.
func StripUnusedFields() toolscache.TransformFunc {
	return func(obj any) (any, error) {
		accessor, err := meta.Accessor(obj)
		if err != nil {
			return obj, nil //nolint:nilerr // non-k8s objects pass through unchanged
		}

		accessor.SetManagedFields(nil)

		annotations := accessor.GetAnnotations()
		if annotations != nil {
			delete(annotations, AnnotationLastAppliedConfiguration)
			accessor.SetAnnotations(annotations)
		}

		return obj, nil
	}
}
