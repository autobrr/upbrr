// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import React, { useEffect, useState } from "react";

interface TrackerIconImageProps {
  tracker: string;
  iconSrc?: string;
  className?: string;
  enabled?: boolean;
}

const trackerAbbreviation = (tracker: string) => {
  const compact = tracker
    .trim()
    .toUpperCase()
    .replaceAll(/[^A-Z0-9]/g, "");
  if (!compact) return "#";
  return compact.slice(0, 3);
};

const getGradient = (value: string) => {
  const code = value.charCodeAt(0) || 0;
  const hue1 = (code * 17) % 360;
  const hue2 = (hue1 + 40) % 360;
  return `linear-gradient(135deg, hsl(${hue1}, 75%, 45%), hsl(${hue2}, 70%, 35%))`;
};

export const TrackerIconImage: React.FC<TrackerIconImageProps> = ({
  tracker,
  iconSrc = "",
  className = "",
  enabled = true,
}) => {
  const [failedSrc, setFailedSrc] = useState("");

  useEffect(() => {
    setFailedSrc("");
  }, [iconSrc]);

  if (!enabled) {
    return null;
  }

  const abbr = trackerAbbreviation(tracker);
  const validIconSrc = iconSrc && failedSrc !== iconSrc ? iconSrc : "";

  return (
    <div
      className={`relative flex h-4 min-w-4 shrink-0 items-center justify-center rounded-[4px] border border-white/10 bg-slate-900/60 px-0.5 text-[8px] font-extrabold uppercase leading-none text-white shadow-[0_1px_2px_rgba(0,0,0,0.4)] ${className}`}
      style={{
        background: validIconSrc ? undefined : getGradient(abbr),
      }}
      aria-hidden="true"
    >
      {validIconSrc ? (
        <img
          src={validIconSrc}
          alt=""
          className="h-full w-full rounded-[3px] object-cover"
          loading="lazy"
          referrerPolicy="no-referrer"
          onError={() => setFailedSrc(validIconSrc)}
        />
      ) : (
        <span className="tracking-normal">{abbr}</span>
      )}
    </div>
  );
};
