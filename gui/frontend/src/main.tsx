// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import React from "react";
import ReactDOM from "react-dom/client";
import WebRoot from "./webRoot";
import "./styles.css";
import "./pages/description_builder/styles.css";
import "./pages/dupe_check/styles.css";
import "./pages/history/styles.css";
import "./pages/input/styles.css";
import "./pages/logging/styles.css";
import "./pages/preparation/styles.css";
import "./pages/screenshots/styles.css";
import "./pages/settings/styles.css";
import "./pages/tracker_data/styles.css";
import "./pages/tracker_upload/styles.css";
import "./pages/upload_images/styles.css";

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <WebRoot />
  </React.StrictMode>,
);
