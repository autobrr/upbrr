import colors from "tailwindcss/colors";

// Match autobrr's palette: zinc mapped onto `gray`, plus the in-between
// shades autobrr generates with tailwind-lerp-colors.
const gray = {
  ...colors.zinc,
  150: "#ececee",
  250: "#dcdcdf",
  550: "#61616a",
  725: "#39393f",
  750: "#333338",
  775: "#2d2d31",
  815: "#232427",
  850: "#1f1f22",
  925: "#141417",
};

/** @type {import("tailwindcss").Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        gray,
      },
      boxShadow: {
        table: "rgba(0, 0, 0, 0.1) 0px 4px 16px 0px",
      },
    },
  },
  plugins: [],
};
