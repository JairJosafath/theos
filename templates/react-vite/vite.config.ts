import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react()],
  test: {
    // Logic lives in modules without React or the DOM, so tests run in
    // plain Node.
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
