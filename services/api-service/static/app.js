/**
 * NewsFlow — Frontend Application
 * Vanilla JS with zero dependencies
 */

const API_BASE = '/user/api';

// ── State ──────────────────────────────────────────────────────
const state = {
    page: 1,
    perPage: 20,
    category: 'all',
    source: '',
    search: '',
    totalPages: 0,
    debounceTimer: null,
};

// ── DOM Elements ───────────────────────────────────────────────
const $grid = document.getElementById('articles-grid');
const $loading = document.getElementById('loading');
const $empty = document.getElementById('empty-state');
const $pagination = document.getElementById('pagination');
const $catTabs = document.getElementById('category-tabs');
const $srcSelect = document.getElementById('source-select');
const $searchInput = document.getElementById('search-input');
const $navbar = document.getElementById('navbar');
const $navStats = document.getElementById('nav-stats');
const $statArticles = document.getElementById('stat-articles');
const $statSources = document.getElementById('stat-sources');
const $statCategories = document.getElementById('stat-categories');

// ── Init ───────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', () => {
    fetchStats();
    fetchCategories();
    fetchSources();
    fetchArticles();
    bindEvents();
});

// ── Event Binding ──────────────────────────────────────────────
function bindEvents() {
    $searchInput.addEventListener('input', () => {
        clearTimeout(state.debounceTimer);
        state.debounceTimer = setTimeout(() => {
            state.search = $searchInput.value.trim();
            state.page = 1;
            fetchArticles();
        }, 400);
    });

    $srcSelect.addEventListener('change', () => {
        state.source = $srcSelect.value;
        state.page = 1;
        fetchArticles();
    });

    window.addEventListener('scroll', () => {
        $navbar.classList.toggle('scrolled', window.scrollY > 40);
    });
}

// ── Fetch Stats ────────────────────────────────────────────────
async function fetchStats() {
    try {
        const res = await fetch(`${API_BASE}/stats`);
        const data = await res.json();
        animateNumber($statArticles, data.total_articles || 0);
        animateNumber($statSources, data.total_sources || 0);
        animateNumber($statCategories, data.total_categories || 0);
        $navStats.textContent = `${data.total_articles || 0} articles`;
    } catch (e) {
        console.warn('Stats fetch failed:', e);
    }
}

function animateNumber(el, target) {
    const duration = 800;
    const start = performance.now();
    const from = 0;
    function update(now) {
        const elapsed = now - start;
        const progress = Math.min(elapsed / duration, 1);
        const eased = 1 - Math.pow(1 - progress, 3);
        el.textContent = Math.round(from + (target - from) * eased);
        if (progress < 1) requestAnimationFrame(update);
    }
    requestAnimationFrame(update);
}

// ── Fetch Categories ───────────────────────────────────────────
async function fetchCategories() {
    try {
        const res = await fetch(`${API_BASE}/categories`);
        const data = await res.json();
        const cats = data.categories || [];

        $catTabs.innerHTML = `<button class="filter-tab active" data-category="all">All</button>`;
        cats.forEach(cat => {
            const btn = document.createElement('button');
            btn.className = 'filter-tab';
            btn.dataset.category = cat.name;
            btn.textContent = `${cat.name} (${cat.count})`;
            $catTabs.appendChild(btn);
        });

        $catTabs.querySelectorAll('.filter-tab').forEach(btn => {
            btn.addEventListener('click', () => {
                $catTabs.querySelectorAll('.filter-tab').forEach(b => b.classList.remove('active'));
                btn.classList.add('active');
                state.category = btn.dataset.category;
                state.page = 1;
                fetchArticles();
            });
        });
    } catch (e) {
        console.warn('Categories fetch failed:', e);
    }
}

// ── Fetch Sources ──────────────────────────────────────────────
async function fetchSources() {
    try {
        const res = await fetch(`${API_BASE}/sources`);
        const data = await res.json();
        const sources = data.sources || [];

        $srcSelect.innerHTML = '<option value="">All Sources</option>';
        sources.forEach(src => {
            const opt = document.createElement('option');
            opt.value = src.name;
            opt.textContent = `${src.name} (${src.count})`;
            $srcSelect.appendChild(opt);
        });
    } catch (e) {
        console.warn('Sources fetch failed:', e);
    }
}

// ── Fetch Articles ─────────────────────────────────────────────
async function fetchArticles() {
    showLoading(true);
    $empty.style.display = 'none';
    $grid.innerHTML = '';

    const params = new URLSearchParams({
        page: state.page,
        per_page: state.perPage,
    });
    if (state.category && state.category !== 'all') params.set('category', state.category);
    if (state.source) params.set('source', state.source);
    if (state.search) params.set('search', state.search);

    try {
        const res = await fetch(`${API_BASE}/articles?${params}`);
        const data = await res.json();

        state.totalPages = data.total_pages || 0;

        showLoading(false);

        if (!data.articles || data.articles.length === 0) {
            $empty.style.display = 'block';
            $pagination.innerHTML = '';
            return;
        }

        renderArticles(data.articles);
        renderPagination(data);
    } catch (e) {
        showLoading(false);
        $empty.style.display = 'block';
        console.error('Articles fetch failed:', e);
    }
}

// ── Render Articles ────────────────────────────────────────────
function renderArticles(articles) {
    $grid.innerHTML = '';
    articles.forEach((article, i) => {
        const card = document.createElement('a');
        card.className = 'article-card';
        card.href = article.url;
        card.target = '_blank';
        card.rel = 'noopener noreferrer';
        card.style.animationDelay = `${i * 0.04}s`;

        const imageHTML = article.image_url
            ? `<img class="card-image" src="${escapeHtml(article.image_url)}" alt="" loading="lazy" onerror="this.outerHTML='<div class=\\'card-image-placeholder\\'>📰</div>'">`
            : `<div class="card-image-placeholder">${getSourceEmoji(article.source)}</div>`;

        card.innerHTML = `
            ${imageHTML}
            <div class="card-body">
                <div class="card-meta">
                    <span class="card-source">${escapeHtml(article.source)}</span>
                    <span class="card-category">${escapeHtml(article.category)}</span>
                </div>
                <h3 class="card-title">${escapeHtml(article.title)}</h3>
                <p class="card-desc">${escapeHtml(article.description || '')}</p>
                <div class="card-footer">
                    <span class="card-time">${formatTime(article.published_at)}</span>
                    <span class="card-readmore">Read →</span>
                </div>
            </div>
        `;
        $grid.appendChild(card);
    });
}

// ── Render Pagination ──────────────────────────────────────────
function renderPagination(data) {
    $pagination.innerHTML = '';
    if (data.total_pages <= 1) return;

    const prevBtn = createPageBtn('‹', state.page > 1, () => {
        state.page--;
        fetchArticles();
        scrollToGrid();
    });
    $pagination.appendChild(prevBtn);

    const pages = getVisiblePages(state.page, data.total_pages);
    pages.forEach(p => {
        if (p === '...') {
            const dot = document.createElement('span');
            dot.className = 'page-btn';
            dot.textContent = '…';
            dot.style.cursor = 'default';
            dot.style.borderColor = 'transparent';
            $pagination.appendChild(dot);
        } else {
            const btn = createPageBtn(p, true, () => {
                state.page = p;
                fetchArticles();
                scrollToGrid();
            });
            if (p === state.page) btn.classList.add('active');
            $pagination.appendChild(btn);
        }
    });

    const nextBtn = createPageBtn('›', state.page < data.total_pages, () => {
        state.page++;
        fetchArticles();
        scrollToGrid();
    });
    $pagination.appendChild(nextBtn);
}

function createPageBtn(text, enabled, onClick) {
    const btn = document.createElement('button');
    btn.className = 'page-btn';
    btn.textContent = text;
    btn.disabled = !enabled;
    if (enabled) btn.addEventListener('click', onClick);
    return btn;
}

function getVisiblePages(current, total) {
    if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1);
    const pages = [];
    pages.push(1);
    if (current > 3) pages.push('...');
    for (let i = Math.max(2, current - 1); i <= Math.min(total - 1, current + 1); i++) {
        pages.push(i);
    }
    if (current < total - 2) pages.push('...');
    pages.push(total);
    return pages;
}

function scrollToGrid() {
    const filters = document.getElementById('filters');
    if (filters) {
        filters.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
}

// ── Helpers ────────────────────────────────────────────────────
function showLoading(show) {
    $loading.style.display = show ? 'flex' : 'none';
}

function escapeHtml(str) {
    if (!str) return '';
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

function formatTime(isoStr) {
    if (!isoStr) return '';
    const date = new Date(isoStr);
    const now = new Date();
    const diff = now - date;
    const mins = Math.floor(diff / 60000);
    const hours = Math.floor(diff / 3600000);
    const days = Math.floor(diff / 86400000);

    if (mins < 1) return 'Just now';
    if (mins < 60) return `${mins}m ago`;
    if (hours < 24) return `${hours}h ago`;
    if (days < 7) return `${days}d ago`;
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
}

function getSourceEmoji(source) {
    const map = {
        'BBC News': '🇬🇧',
        'The Guardian': '📰',
        'Al Jazeera': '🌍',
        'NPR News': '🎙️',
        'TechCrunch': '💻',
        'ESPN': '⚽',
        'BBC Science': '🔬',
        'BBC Business': '💼',
        'BBC Health': '🏥',
    };
    return map[source] || '📄';
}

// ── Auto-refresh every 2 minutes ───────────────────────────────
setInterval(() => {
    fetchStats();
    if (state.page === 1) fetchArticles();
}, 120000);
