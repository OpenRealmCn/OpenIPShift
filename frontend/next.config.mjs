/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'standalone',
  // Bound build concurrency for small CI/development machines.
  experimental: { cpus: 2 },
};
export default nextConfig;
