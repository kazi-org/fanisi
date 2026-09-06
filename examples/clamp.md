Fix Clamp so values below the minimum return the minimum. Preserve upper-bound
and in-range behavior. Callers guarantee min <= max. Make the smallest clear fix
in clamp.go and retain its public function and documentation. The existing test
file is read-only acceptance evidence. Run the configured formatter and verifier.
