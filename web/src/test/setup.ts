import "@testing-library/jest-dom/vitest";

// jsdom doesn't implement scrollIntoView
window.HTMLElement.prototype.scrollIntoView = () => {};
