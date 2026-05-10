import type { Config } from 'tailwindcss';

export default {
  content: ['./src/**/*.{astro,html,ts,tsx,md,mdx}'],
  theme: {
    extend: {
      colors: {
        md: {
          primary: '#006d52',
          'on-primary': '#ffffff',
          'primary-container': '#9af5cd',
          'on-primary-container': '#002115',
          surface: '#f6fbf6',
          'surface-container-lowest': '#ffffff',
          'surface-container-low': '#f0f6f1',
          'surface-container': '#eaf0eb',
          'surface-container-high': '#e4eae5',
          'on-surface': '#181d1a',
          'on-surface-variant': '#404943',
          outline: '#707972',
          'outline-variant': '#c0c9c2',
          error: '#d93025',
          'error-container': '#fce8e6',
          'on-error-container': '#c5221f',
          tertiary: '#3c6473',
          'tertiary-container': '#c0e9fb',
        },
      },
      fontFamily: {
        sans: ['Inter', 'system-ui', 'sans-serif'],
      },
      maxWidth: {
        content: '1200px',
        hero: '1080px',
      },
      borderRadius: {
        'm3-sm': '8px',
        'm3-md': '12px',
        'm3-lg': '16px',
        'm3-xl': '28px',
      },
      boxShadow: {
        'm3-1': '0 1px 2px 0 rgb(0 0 0 / 0.05), 0 1px 3px 1px rgb(0 0 0 / 0.05)',
        'm3-2': '0 1px 2px 0 rgb(0 0 0 / 0.06), 0 2px 6px 2px rgb(0 0 0 / 0.08)',
        'm3-3': '0 4px 8px 3px rgb(0 0 0 / 0.08), 0 1px 3px 0 rgb(0 0 0 / 0.06)',
      },
    },
  },
  plugins: [],
} satisfies Config;
