import { defineConfig, loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const backend = env.VITE_BIRDY_BACKEND || 'http://127.0.0.1:8787';

  return {
    build: {
      outDir: 'dist',
      emptyOutDir: true,
    },
    server: {
      host: true,
      proxy: {
        '/ws': {
          target: backend,
          changeOrigin: false,
          ws: true,
        },
        '/api': {
          target: backend,
          changeOrigin: true,
        },
      },
    },
  };
});
