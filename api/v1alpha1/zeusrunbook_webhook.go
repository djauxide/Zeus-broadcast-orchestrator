
package v1alpha1

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var runbooklog = logf.Log.WithName("zeusrunbook-webhook")

func (r *ZeusRunbook) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(&ZeusRunbookValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-core-zeus-broadcast-v1alpha1-zeusrunbook,mutating=false,failurePolicy=fail,sideEffects=None,groups=core.zeus.broadcast,resources=zeusrunbooks,verbs=create;update,versions=v1alpha1,name=vzeusrunbook.kb.io,admissionReviewVersions=v1

type ZeusRunbookValidator struct{}

var _ webhook.CustomValidator = &ZeusRunbookValidator{}

func (v *ZeusRunbookValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	rb, ok := obj.(*ZeusRunbook)
	if !ok {
		return nil, fmt.Errorf("expected ZeusRunbook, got %T", obj)
	}
	return nil, validateContribution(rb)
}

func (v *ZeusRunbookValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	rb, ok := newObj.(*ZeusRunbook)
	if !ok {
		return nil, fmt.Errorf("expected ZeusRunbook, got %T", newObj)
	}
	return nil, validateContribution(rb)
}

func (v *ZeusRunbookValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateContribution(rb *ZeusRunbook) error {
	if rb.Spec.Ingress == nil || rb.Spec.Ingress.Contribution == nil {
		return nil
	}
	c := rb.Spec.Ingress.Contribution
	var errs field.ErrorList
	path := field.NewPath("spec", "ingress", "contribution")

	if len(c.Paths) == 0 {
		errs = append(errs, field.Required(path.Child("paths"), "at least one WAN path required"))
	}

	seen := map[string]bool{}
	for i, p := range c.Paths {
		pPath := path.Child("paths").Index(i)
		if p.Name == "" {
			errs = append(errs, field.Required(pPath.Child("name"), "path name required"))
		}
		if seen[p.Name] {
			errs = append(errs, field.Duplicate(pPath.Child("name"), p.Name))
		}
		seen[p.Name] = true

		switch p.Transport {
		case "srt":
			if p.SRT == nil {
				errs = append(errs, field.Required(pPath.Child("srt"), "srt spec required when transport=srt"))
				continue
			}
			errs = append(errs, validateSRT(p.SRT, pPath.Child("srt"))...)
			errs = append(errs, validateSecretRef(&p.SRT.PassphraseSecretRef, pPath.Child("srt", "passphraseSecretRef"))...)
		case "rist":
			if p.RIST == nil {
				errs = append(errs, field.Required(pPath.Child("rist"), "rist spec required when transport=rist"))
				continue
			}
			if p.RIST.Profile < 0 || p.RIST.Profile > 2 {
				errs = append(errs, field.Invalid(pPath.Child("rist", "profile"), p.RIST.Profile, "must be 0, 1, or 2"))
			}
			if c.MergeMode == "hitless" && p.RIST.Profile != 1 {
				errs = append(errs, field.Invalid(pPath.Child("rist", "profile"), p.RIST.Profile,
					"hitless merge requires RIST profile 1 (Main), which is ST 2022-7 capable"))
			}
			if p.RIST.PassphraseSecretRef != nil {
				errs = append(errs, validateSecretRef(p.RIST.PassphraseSecretRef, pPath.Child("rist", "passphraseSecretRef"))...)
			}
		default:
			errs = append(errs, field.NotSupported(pPath.Child("transport"), p.Transport, []string{"srt", "rist"}))
		}
	}

	if c.Redundancy == "hitless" && len(c.Paths) < 2 {
		errs = append(errs, field.Invalid(path.Child("redundancy"), c.Redundancy, "hitless merge requires at least 2 paths"))
	}

	if len(errs) > 0 {
		return apierrors.NewInvalid(GroupVersion.WithKind("ZeusRunbook").GroupKind(), rb.Name, errs)
	}
	return nil
}

func validateSRT(s *SRTSpec, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	switch s.Mode {
	case SRTCaller, SRTRendezvous:
		if s.RemoteHost == "" {
			errs = append(errs, field.Required(path.Child("remoteHost"), "required for caller/rendezvous"))
		}
		if s.RemotePort <= 0 || s.RemotePort > 65535 {
			errs = append(errs, field.Invalid(path.Child("remotePort"), s.RemotePort, "must be 1-65535"))
		}
	case SRTListener:
		if s.LocalPort <= 0 || s.LocalPort > 65535 {
			errs = append(errs, field.Invalid(path.Child("localPort"), s.LocalPort, "must be 1-65535 for listener"))
		}
	default:
		errs = append(errs, field.NotSupported(path.Child("mode"), s.Mode, []string{"caller", "listener", "rendezvous"}))
	}
	if s.PBKeyLen == 0 {
		errs = append(errs, field.Invalid(path.Child("pbKeyLen"), 0,
			"pbKeyLen is required when passphraseSecretRef is set; 0 disables AES"))
	} else if s.PBKeyLen != 16 && s.PBKeyLen != 24 && s.PBKeyLen != 32 {
		errs = append(errs, field.NotSupported(path.Child("pbKeyLen"), s.PBKeyLen, []string{"16", "24", "32"}))
	}
	return errs
}

func validateSecretRef(ref *corev1.SecretKeySelector, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if ref.Name == "" {
		errs = append(errs, field.Required(path.Child("name"), "secret name required"))
	}
	if ref.Key == "" {
		errs = append(errs, field.Required(path.Child("key"), "secret key required"))
	}
	return errs
}
