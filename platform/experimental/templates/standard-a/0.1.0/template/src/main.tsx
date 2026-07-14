import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { AppShell } from "./generated/AppShell";
import { discoverCustomRoutes } from "./integration/routes";
import "./generated/theme.css";

const productName = {{json:blueprint.product.name}};
const routes = discoverCustomRoutes();

createRoot(document.getElementById("root")!).render(<StrictMode><AppShell productName={productName} routes={routes} /></StrictMode>);
