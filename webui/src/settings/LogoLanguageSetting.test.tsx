// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { LogoLanguageSetting } from "./LogoLanguageSetting";

it("searches Brazil, retains preference order and never adds custom languages", async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  function Setting() {
    const [value, setValue] = useState("JA;zz");
    return (
      <LogoLanguageSetting
        id="logo-languages"
        label="Preferred logo languages"
        value={value}
        onChange={(next) => {
          setValue(next);
          onChange(next);
        }}
      />
    );
  }
  render(<Setting />);
  expect(screen.getByText(/unsupported logo languages.*zz/i)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Preferred logo languages" }));
  const search = screen.getByRole("searchbox", { name: /search preferred logo languages/i });
  await user.type(search, "Brazil");
  await user.click(screen.getByRole("checkbox", { name: /Brazilian Portuguese/ }));
  expect(onChange).toHaveBeenLastCalledWith("ja,zz,pt");
  await user.clear(search);
  await user.type(search, "Example custom language");
  expect(screen.queryByRole("button", { name: /add example/i })).not.toBeInTheDocument();
  await user.keyboard("{Escape}");
  expect(screen.getByRole("button", { name: "Preferred logo languages" })).toHaveFocus();
});
