# Toolchain performance budgets

`budgets.json` records the reviewed latency, allocation, and memory ceilings for
the toolchain's critical build, generation, editor, CLI, and development-loop
paths. `make benchmark` runs only this contract; `make verify` runs the same
contract as a mandatory release gate. Both use the committed vendor graph with
workspace and network module resolution disabled.

The gate executes each benchmark five times for 250 ms with memory statistics
enabled and evaluates the median of each metric. Median sampling reduces
transient scheduler noise, while deliberately generous latency ceilings keep
the contract portable across supported shared CI hosts. Allocation and byte
ceilings catch deterministic structural regressions independently of host
speed.

`reference_ns_per_op` documents the reviewed Go 1.26.5 baseline. It is context,
not an automatically moving target. A ceiling may change only with a measured
implementation change and an adjacent reviewed rationale. Do not raise a
ceiling merely to make a failing run green.
