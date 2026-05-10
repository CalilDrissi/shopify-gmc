import { defineConfig } from 'astro/config';
import tailwind from '@astrojs/tailwind';

export default defineConfig({
  site: 'https://shopifygmc.com',
  integrations: [tailwind({ applyBaseStyles: false })],
  build: {
    assets: '_astro',
    inlineStylesheets: 'auto',
  },
  vite: {
    server: { fs: { strict: false } },
  },
});
