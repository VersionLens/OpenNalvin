import { formatCompactTokens } from "@/lib/utils";

describe("formatCompactTokens", () => {
  it("formats compact token counts", () => {
    expect(formatCompactTokens(1500)).toBe("1.5k");
    expect(formatCompactTokens(3000)).toBe("3k");
    expect(formatCompactTokens(45_000)).toBe("45k");
  });
});
