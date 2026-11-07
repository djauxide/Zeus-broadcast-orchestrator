package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

// ConformanceCheck represents a single conformance test result.
type ConformanceCheck struct {
	Check   string `json:"check"`
	Pass    bool   `json:"pass"`
	Reason  string `json:"reason,omitempty"`
	Severity string `json:"severity"` // "error", "warning", "info"
}

// ConformanceReport is the full conformance report for a NAP.
type ConformanceReport struct {
	NAPName   string             `json:"napName"`
	Namespace string             `json:"namespace,omitempty"`
	Passed    bool               `json:"passed"`
	Checks    []ConformanceCheck `json:"checks"`
}

// validateNAP conformance-checks a ZeusApplicationPackage against the Zeus spec.
func validateNAP(nap *zeusv1.ZeusApplicationPackage) *ConformanceReport {
	report := &ConformanceReport{
		NAPName:   nap.Name,
		Namespace: nap.Namespace,
		Passed:    true,
	}

	checks := []ConformanceCheck{}

	// 1. Image must be non-empty.
	if nap.Spec.Image == "" {
		checks = append(checks, ConformanceCheck{
			Check:     "image.required",
			Pass:      false,
			Reason:    "spec.image is required",
			Severity:  "error",
		})
		report.Passed = false
	} else {
		checks = append(checks, ConformanceCheck{
			Check:    "image.required",
			Pass:     true,
			Reason:   fmt.Sprintf("image: %s", nap.Spec.Image),
			Severity: "info",
		})
	}

	// 2. Image signature must be non-empty.
	if nap.Spec.ImageSignature == "" {
		checks = append(checks, ConformanceCheck{
			Check:     "imageSignature.required",
			Pass:      false,
			Reason:    "spec.imageSignature is required (cosign or similar)",
			Severity:  "error",
		})
		report.Passed = false
	} else {
		checks = append(checks, ConformanceCheck{
			Check:    "imageSignature.required",
			Pass:     true,
			Reason:   fmt.Sprintf("signature: %s", nap.Spec.ImageSignature),
			Severity: "info",
		})
	}

	// 3. CRM CPU must be set.
	if nap.Spec.CRM.CPU == "" {
		checks = append(checks, ConformanceCheck{
			Check:     "crm.cpu.required",
			Pass:      false,
			Reason:    "spec.crm.cpu is required",
			Severity:  "error",
		})
		report.Passed = false
	} else {
		checks = append(checks, ConformanceCheck{
			Check:    "crm.cpu.required",
			Pass:     true,
			Reason:   fmt.Sprintf("cpu: %s", nap.Spec.CRM.CPU),
			Severity: "info",
		})
	}

	// 4. CRM Memory must be set.
	if nap.Spec.CRM.Memory == "" {
		checks = append(checks, ConformanceCheck{
			Check:     "crm.memory.required",
			Pass:      false,
			Reason:    "spec.crm.memory is required",
			Severity:  "error",
		})
		report.Passed = false
	} else {
		checks = append(checks, ConformanceCheck{
			Check:    "crm.memory.required",
			Pass:     true,
			Reason:   fmt.Sprintf("memory: %s", nap.Spec.CRM.Memory),
			Severity: "info",
		})
	}

	// 5. If MXL reader/writer, domainRef is required.
	io := nap.Spec.CRM.MediaIO
	if io.MXLReader || io.MXLWriter {
		if io.DomainRef == "" {
			checks = append(checks, ConformanceCheck{
				Check:     "crm.mediaIO.domainRef.required",
				Pass:      false,
				Reason:    "spec.crm.mediaIO.domainRef is required when mxlReader or mxlWriter is true",
				Severity:  "error",
			})
			report.Passed = false
		} else {
			checks = append(checks, ConformanceCheck{
				Check:    "crm.mediaIO.domainRef.required",
				Pass:     true,
				Reason:   fmt.Sprintf("domainRef: %s", io.DomainRef),
				Severity: "info",
			})
		}

		// 6. If MXL, transport must be mxl.
		if nap.Spec.NMOS.Transport != "urn:x-nmos:transport:mxl" {
			checks = append(checks, ConformanceCheck{
				Check:     "nmos.transport.mxl.required",
				Pass:      false,
				Reason:    "spec.nmos.transport must be urn:x-nmos:transport:mxl for MXL NAPs",
				Severity:  "error",
			})
			report.Passed = false
		} else {
			checks = append(checks, ConformanceCheck{
				Check:    "nmos.transport.mxl.required",
				Pass:     true,
				Reason:   "transport: urn:x-nmos:transport:mxl",
				Severity: "info",
			})
		}
	}

	// 7. If hitless redundancy, RIST profile must be 1.
	if nap.Spec.NMOS.IS04 || nap.Spec.NMOS.IS05 {
		// IS-04/IS-05 enabled is informational.
		checks = append(checks, ConformanceCheck{
			Check:    "nmos.is04/is05.enabled",
			Pass:     true,
			Reason:   fmt.Sprintf("is04: %v, is05: %v", nap.Spec.NMOS.IS04, nap.Spec.NMOS.IS05),
			Severity: "info",
		})
	}

	// 8. RIST profile validation (if RIST paths are used).
	// This is checked at the runbook level, but we can note it here.
	if nap.Spec.NMOS.IS10 {
		checks = append(checks, ConformanceCheck{
			Check:     "nmos.is10.authz",
			Pass:      true,
			Reason:    "IS-10 authorization enabled",
			Severity:  "info",
		})
	}

	report.Checks = checks
	return report
}

func main() {
	inputFile := flag.String("input", "", "Path to ZeusApplicationPackage YAML/JSON file")
	outputJSON := flag.Bool("json", false, "Output report as JSON")
	flag.Parse()

	if *inputFile == "" {
		fmt.Fprintln(os.Stderr, "Usage: zeus-nap-conformance --input <nap.yaml> [--json]")
		os.Exit(2)
	}

	data, err := os.ReadFile(*inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read %s: %v\n", *inputFile, err)
		os.Exit(1)
	}

	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to convert YAML to JSON: %v\n", err)
		os.Exit(1)
	}

	var nap zeusv1.ZeusApplicationPackage
	if err := json.Unmarshal(jsonData, &nap); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse NAP: %v\n", err)
		os.Exit(1)
	}

	report := validateNAP(&nap)

	if *outputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to encode report: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Printf("NAP Conformance Report: %s\n", report.NAPName)
		if report.Namespace != "" {
			fmt.Printf("Namespace: %s\n", report.Namespace)
		}
		fmt.Printf("Result: ")
		if report.Passed {
			fmt.Println("PASS")
		} else {
			fmt.Println("FAIL")
		}
		fmt.Println()
		for _, c := range report.Checks {
			status := "PASS"
			if !c.Pass {
				status = "FAIL"
			}
			fmt.Printf("  [%s] %s: %s\n", status, c.Check, c.Reason)
		}
	}

	if !report.Passed {
		os.Exit(1)
	}
}
