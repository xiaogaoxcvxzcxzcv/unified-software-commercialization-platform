# Controlled experimental product extensions

Experimental extension manifests live at `<extension_id>/<strict-semver>/manifest.json`.
Only server-published `experimental` and `verified` entries are accepted by this loader. This root is physically separate from the ordinary catalog and cannot be selected through client-supplied scope.
