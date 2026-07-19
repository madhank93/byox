// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import sitemap from '@astrojs/sitemap';

// Served from the custom domain at the root.
const SITE = 'https://byox.madhan.app';
const BASE = '/';
const DESCRIPTION =
	'Complete CodeCrafters "Build your own X" courses entirely locally, in Go — with tester-verified reference solutions for every stage.';

export default defineConfig({
	site: SITE,
	base: BASE,
	integrations: [
		sitemap(),
		starlight({
			title: 'byox',
			description: DESCRIPTION,
			customCss: ['./src/styles/hero.css'],
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/madhank93/build-your-own-x' },
			],
			editLink: {
				baseUrl: 'https://github.com/madhank93/build-your-own-x/edit/main/web/',
			},
			// SEO / social-share metadata applied to every page.
			head: [
				{ tag: 'meta', attrs: { property: 'og:type', content: 'website' } },
				{ tag: 'meta', attrs: { property: 'og:site_name', content: 'byox' } },
				{ tag: 'meta', attrs: { property: 'og:image', content: new URL(`${BASE}favicon.svg`, SITE).href } },
				{ tag: 'meta', attrs: { name: 'twitter:card', content: 'summary' } },
				{ tag: 'meta', attrs: { name: 'twitter:image', content: new URL(`${BASE}favicon.svg`, SITE).href } },
				{ tag: 'meta', attrs: { name: 'theme-color', content: '#00add8' } },
				{
					tag: 'meta',
					attrs: {
						name: 'keywords',
						content:
							'CodeCrafters, build your own redis, build your own kafka, build your own interpreter, Go, Golang, rustlings, coding challenges, learn Go, systems programming',
					},
				},
			],
			sidebar: [
				{ label: 'Getting started', slug: 'getting-started' },
				{ label: 'Catalog', link: '/catalog/' },
			],
		}),
	],
});
