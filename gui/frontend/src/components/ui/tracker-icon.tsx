// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import React, { useState, useEffect } from "react";
import { getTrackerDomain } from "../../utils/favicon";

interface TrackerIconImageProps {
  tracker: string;
  customUrl?: string;
  className?: string;
  enabled?: boolean;
}

export const TrackerIconImage: React.FC<TrackerIconImageProps> = ({
  tracker,
  customUrl,
  className = "",
  enabled = true,
}) => {
  const [iconSrc, setIconSrc] = useState<string | null>(null);
  const [hasError, setHasError] = useState(false);

  const trimmed = tracker.trim();
  const fallbackLetter = trimmed ? trimmed.charAt(0).toUpperCase() : "#";
  const domain = getTrackerDomain(trimmed, customUrl);

  useEffect(() => {
    setIconSrc(null);
    setHasError(false);

    if (!enabled || !domain) {
      return;
    }

    let active = true;
    const fetchIcon = async () => {
      try {
        const getTrackerIcon = globalThis.go?.guiapp?.App?.GetTrackerIcon;
        if (getTrackerIcon) {
          const dataUrl = await getTrackerIcon(domain, customUrl || "");
          if (active) {
            if (dataUrl) {
              setIconSrc(dataUrl);
            } else {
              setHasError(true);
            }
          }
        } else {
          if (active) {
            setHasError(true);
          }
        }
      } catch {
        if (active) {
          setHasError(true);
        }
      }
    };

    fetchIcon();

    return () => {
      active = false;
    };
  }, [domain, customUrl, enabled]);

  // Generate an aesthetically pleasing dynamic HSL gradient based on the first letter of the tracker name
  const getGradient = (letter: string) => {
    const code = letter.charCodeAt(0) || 0;
    const hue1 = (code * 17) % 360;
    const hue2 = (hue1 + 40) % 360;
    return `linear-gradient(135deg, hsl(${hue1}, 75%, 45%), hsl(${hue2}, 70%, 35%))`;
  };

  if (!enabled) {
    return null;
  }

  return (
    <div
      className={`relative flex h-4 w-4 shrink-0 items-center justify-center rounded-[4px] border border-white/10 bg-slate-900/60 text-[10px] font-bold uppercase leading-none shadow-[0_1px_2px_rgba(0,0,0,0.4)] ${className}`}
      style={{
        background: !iconSrc || hasError ? getGradient(fallbackLetter) : undefined,
      }}
    >
      {iconSrc && !hasError ? (
        <img
          src={iconSrc}
          alt=""
          className="h-full w-full rounded-[3px] object-cover"
          loading="lazy"
          referrerPolicy="no-referrer"
          onError={() => setHasError(true)}
        />
      ) : (
        <span className="text-white text-[9px] font-extrabold tracking-tighter" aria-hidden="true">
          {fallbackLetter}
        </span>
      )}
    </div>
  );
};
