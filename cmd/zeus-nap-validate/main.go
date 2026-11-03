package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: zeus-nap-validate <nap.yaml> [pub-key-path]")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail("read: %v", err)
	}

	var nap zeusv1.ZeusApplicationPackage
	if err := json.Unmarshal(data, &nap); err != nil {
		fail("parse: %v", err)
	}

	if nap.Spec.Image == "" || nap.Spec.ImageSignature == "" {
		fail("image and imageSignature are required")
	}
	if nap.Spec.CRM.CPU == "" || nap.Spec.CRM.Memory == "" {
		fail("crm.cpu and crm.memory are required")
	}
	io := nap.Spec.CRM.MediaIO
	if (io.MXLReader || io.MXLWriter) && io.DomainRef == "" {
		fail("MXL NAPs must declare crm.mediaIO.domainRef")
	}
	if (io.MXLReader || io.MXLWriter) && nap.Spec.NMOS.Transport != "urn:x-nmos:transport:mxl" {
		fail("MXL NAPs must declare nmos.transport: urn:x-nmos:transport:mxl (BCP-007-03)")
	}
	if len(os.Args) >= 3 {
		cmd := exec.Command("cosign", "verify", "--key", os.Args[2], nap.Spec.Image)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fail("cosign verify failed: %v", err)
		}
	} else {
		fmt.Fprintln(os.Stderr, "WARN: no public key supplied; skipping cosign verification")
	}
	fmt.Printf("PASS: %s (%s)\n", nap.Name, nap.Spec.Image)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
