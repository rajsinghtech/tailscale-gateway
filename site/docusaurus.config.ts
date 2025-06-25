import type { Config } from '@docusaurus/types';

const config: Config = {
  title: 'Tailscale Gateway',
  tagline: 'Documentation for Tailscale Gateway Kubernetes Operator',
  url: 'https://example.com',
  baseUrl: '/',
  favicon: 'img/favicon.ico',

  // GitHub pages deployment config (update as needed)
  organizationName: 'tailscale',
  projectName: 'tailscale-gateway',

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: require.resolve('./sidebars.ts'),
          editUrl: 'https://github.com/tailscale/tailscale-gateway/tree/main/site/',
        },
        blog: false,
        theme: {
          customCss: require.resolve('./src/css/custom.css'),
        },
      },
    ],
  ],

  themeConfig: {
    navbar: {
      title: 'Tailscale Gateway',
      logo: {
        alt: 'Tailscale Gateway',
        src: 'img/logo.svg',
      },
      items: [
        {
          type: 'doc',
          docId: 'intro',
          position: 'left',
          label: 'Docs',
        },
        {
          href: 'https://github.com/tailscale/tailscale-gateway',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            {
              label: 'Introduction',
              to: '/docs/intro',
            },
          ],
        },
        {
          title: 'Community',
          items: [
            {
              label: 'GitHub Issues',
              href: 'https://github.com/tailscale/tailscale-gateway/issues',
            },
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Tailscale`,
    },
  },
};

export default config; 