import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { HostedApp } from "./HostedApp";

const root = document.getElementById("root");
if (!root) throw new Error("hosted root is unavailable");
const browserFetch = window.fetch.bind(window);
createRoot(root).render(<StrictMode><HostedApp fetch={browserFetch} /></StrictMode>);
