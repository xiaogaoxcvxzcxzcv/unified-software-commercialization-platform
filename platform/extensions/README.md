# Trusted product extensions

Production extension manifests live at `<extension_id>/<strict-semver>/manifest.json`.
Only server-published `ordinary` and `available` entries are accepted by the production loader. This catalog is intentionally empty until an extension passes the complete installation, lifecycle, isolation and regression gates.
