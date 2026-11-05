# Minimal stand-in for a commercial MXL NAP, used only to verify the Zeus
# deploy path end-to-end on a local kind cluster. It implements no real MXL
# data plane; it merely runs long enough for the pod to become Ready so the
# reconciler can bind an MxlReceiver's podRef to a live pod.
FROM docker.io/library/busybox:1.36
USER 65534:65534
CMD ["sleep", "infinity"]
