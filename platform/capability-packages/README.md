# Ordinary Capability Package Catalog

Only complete capability package versions whose declared target, delivery mode and environment combinations are `ordinary + available` may be published here.

Layout:

```text
<package_id>/<version>/manifest.json
```

The directory is intentionally empty until a real package passes all nine delivery surfaces, target E2E, upgrade/rollback and old-product regression gates. Schema fixtures and machine catalog tests never count as published packages.
