const state = {
  consignments: [],
  selectedId: null,
  current: null,
  summary: null,
  inventory: [],
  settlement: null,
  overview: null,
  editingSKU: null,
  filter: { state: "", query: "", sort: "newest" }
};

const elements = {
  list: document.querySelector("#consignment-list"),
  workspace: document.querySelector("#workspace-content"),
  title: document.querySelector("#workspace-title"),
  pill: document.querySelector("#state-pill"),
  toast: document.querySelector("#toast"),
  total: document.querySelector("#metric-total"),
  active: document.querySelector("#metric-active"),
  settled: document.querySelector("#metric-settled"),
  metricState: document.querySelector("#metric-state"),
  metricVersion: document.querySelector("#metric-version")
};

document.querySelector("#today-label").textContent = new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit" }).format(new Date());
document.querySelector("#refresh-button").addEventListener("click", () => loadList(true));
document.querySelector("#list-state").addEventListener("change", event => {
  state.filter.state = event.target.value;
  loadList(false);
});
document.querySelector("#list-sort").addEventListener("change", event => {
  state.filter.sort = event.target.value;
  loadList(false);
});
document.querySelector("#list-filters").addEventListener("submit", event => event.preventDefault());
document.querySelector("#list-query").addEventListener("input", event => {
  clearTimeout(loadList.searchTimer);
  state.filter.query = event.target.value.trim();
  loadList.searchTimer = setTimeout(() => loadList(false), 220);
});
loadList(false);

async function request(path, options = {}) {
  const response = await fetch(path, { headers: { "Content-Type": "application/json" }, ...options });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.error || "请求无法处理");
  return payload;
}

async function loadList(showMessage) {
  try {
    const params = new URLSearchParams();
    if (state.filter.state) params.set("state", state.filter.state);
    if (state.filter.query) params.set("q", state.filter.query);
    params.set("sort", state.filter.sort);
    const payload = await request(`/api/consignments?${params}`);
    state.consignments = payload.consignments || [];
    state.overview = payload.overview || null;
    if (state.selectedId && !state.consignments.some(item => item.id === state.selectedId)) state.selectedId = null;
    if (!state.selectedId && state.consignments.length) state.selectedId = state.consignments[0].id;
    await loadCurrent();
    renderList();
    renderMetrics();
    if (showMessage) toast("账本已刷新");
  } catch (error) {
    toast(error.message);
  }
}

async function loadCurrent() {
  if (!state.selectedId) {
    state.current = null;
    state.summary = null;
    state.inventory = [];
    state.settlement = null;
    renderWorkspace();
    return;
  }
  try {
    const payload = await request(`/api/consignments/${encodeURIComponent(state.selectedId)}`);
    state.current = payload.consignment;
	state.current.items = state.current.items || [];
	state.current.sales = state.current.sales || [];
    state.summary = payload.summary || null;
    state.inventory = payload.inventory || [];
    state.settlement = payload.settlement_preview || null;
    renderWorkspace();
  } catch (error) {
    toast(error.message);
  }
}

function renderMetrics() {
  const overview = state.overview;
  elements.total.textContent = overview ? overview.total_consignments : state.consignments.length;
  elements.active.textContent = overview ? overview.active_consignments : state.consignments.filter(item => item.state === "active").length;
  elements.settled.textContent = overview ? overview.settled_consignments : state.consignments.filter(item => item.state === "settled").length;
  if (!state.current) {
    elements.metricState.textContent = "准备开始";
    elements.metricVersion.textContent = "等待选择寄售单";
    return;
  }
  elements.metricState.textContent = stateLabel(state.current.state);
  elements.metricVersion.textContent = `版本 ${state.current.version} / ${state.current.id}`;
}

function renderList() {
  if (!state.consignments.length) {
    const filtered = state.filter.state || state.filter.query;
    const message = filtered ? "没有匹配的寄售单" : "账本还是空的";
    const detail = filtered ? "调整筛选条件后再查看。" : "创建第一份寄售单开始记录。";
    elements.list.innerHTML = `<div class="empty-state"><strong>${message}</strong><span>${detail}</span></div>`;
    return;
  }
  elements.list.innerHTML = state.consignments.map(item => `<button class="list-item ${item.id === state.selectedId ? "selected" : ""}" data-id="${escapeHTML(item.id)}" type="button"><span><strong>${escapeHTML(item.seller_name)}</strong><small>${escapeHTML(item.id)}</small></span><span class="list-state ${item.state}">${stateLabel(item.state)}</span></button>`).join("");
  elements.list.querySelectorAll("[data-id]").forEach(button => button.addEventListener("click", async () => {
    state.selectedId = button.dataset.id;
    state.editingSKU = null;
    await loadCurrent();
    renderList();
    renderMetrics();
  }));
}

function renderWorkspace() {
  const current = state.current;
  elements.pill.className = `state-pill state-${current ? current.state : "draft"}`;
  elements.pill.textContent = current ? stateLabel(current.state) : "草稿";
  if (!current) {
    elements.title.textContent = "建立一份寄售单";
    elements.workspace.innerHTML = createForm();
    document.querySelector("#create-form").addEventListener("submit", createConsignment);
    return;
  }
  if (state.editingSKU && !current.items.some(item => item.sku === state.editingSKU)) state.editingSKU = null;
  elements.title.textContent = current.seller_name;
  if (current.state === "draft") elements.workspace.innerHTML = draftView(current);
  if (current.state === "active") elements.workspace.innerHTML = activeView(current);
  if (current.state === "settled") elements.workspace.innerHTML = settledView(current);
  bindCurrentActions(current);
}

function createForm() {
  return `<div class="form-layout"><p class="form-intro">先建立一份草稿，登记摊主与本次寄售的佣金率。</p><form id="create-form"><div class="form-grid"><div class="field field-wide"><label for="seller-name">摊主名称</label><input id="seller-name" name="seller_name" maxlength="200" placeholder="例如：林间手作" required></div><div class="field"><label for="commission-bps">佣金率（万分比）</label><input id="commission-bps" name="commission_bps" type="number" min="0" max="10000" step="1" value="1000" required><span class="field-hint">1000 = 10%</span></div></div><div class="button-row"><button class="button" type="submit"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>建立草稿</button></div></form></div>`;
}

function draftView(current) {
  const editing = current.items.find(item => item.sku === state.editingSKU);
  const values = editing || { display_sku: "", name: "", initial_quantity: "", unit_price_cents: 0 };
  const price = editing ? (values.unit_price_cents / 100).toFixed(2) : "";
  const heading = editing ? "修改商品" : "商品清单";
  const cancel = editing ? '<button class="button button-quiet" id="cancel-edit" type="button">取消修改</button>' : "";
  const iconPath = editing ? "m5 12 4 4L19 6" : "M12 5v14M5 12h14";
  const submitLabel = editing ? "保存" : "加入";
  return `<form class="terms-form" id="terms-form"><div class="field"><label for="draft-seller">摊主名称</label><input id="draft-seller" name="seller_name" maxlength="200" value="${escapeHTML(current.seller_name)}" required></div><div class="field"><label for="draft-commission">佣金万分比</label><input id="draft-commission" name="commission_bps" type="number" min="0" max="10000" step="1" value="${current.commission_bps}" required></div><button class="icon-button" type="submit" title="保存基本信息" aria-label="保存基本信息"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h12l2 2v14H5zM8 4v6h8V4M8 20v-6h8v6"/></svg></button></form><div class="item-toolbar"><div><h3>${heading}</h3><span class="item-count">${current.items.length} 个商品 / 版本 ${current.version}</span></div>${cancel}</div><form class="item-form" id="item-form"><div class="field"><label for="item-sku">商品编号</label><input id="item-sku" name="sku" maxlength="64" value="${escapeHTML(values.display_sku)}" placeholder="如 A-001" required></div><div class="field"><label for="item-name">商品名称</label><input id="item-name" name="name" maxlength="200" value="${escapeHTML(values.name)}" placeholder="如 手工陶杯" required></div><div class="field"><label for="item-quantity">数量</label><input id="item-quantity" name="quantity" type="number" min="1" max="1000000" step="1" value="${values.initial_quantity}" required></div><div class="field"><label for="item-price">单价（元）</label><input id="item-price" name="unit_price" inputmode="decimal" value="${price}" placeholder="如 88.00" required></div><button class="button" type="submit"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="${iconPath}"/></svg>${submitLabel}</button></form>${itemTable(current)}<div class="action-zone"><button class="button button-secondary" id="activate-button" type="button" ${current.items.length ? "" : "disabled"}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m5 12 4 4L19 6"/></svg>确认清单并上架</button></div>`;
}

function itemTable(current) {
  if (!current.items.length) return '<div class="empty-state"><strong>还没有商品</strong><span>录入商品后才能确认上架。</span></div>';
  const actions = current.state === "draft";
  const actionHeading = actions ? '<th><span class="sr-only">操作</span></th>' : "";
  const rows = current.items.map(item => {
    const buttons = actions ? `<td class="row-actions"><button class="icon-button edit-item" data-sku="${escapeHTML(item.sku)}" type="button" title="修改商品" aria-label="修改 ${escapeHTML(item.name)}"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m4 20 4.5-1 10-10-3.5-3.5-10 10L4 20Zm9-12 3.5 3.5"/></svg></button><button class="icon-button remove-item" data-sku="${escapeHTML(item.sku)}" type="button" title="删除商品" aria-label="删除 ${escapeHTML(item.name)}"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M9 7V4h6v3m-9 0 1 13h10l1-13M10 11v5m4-5v5"/></svg></button></td>` : "";
    return `<tr><td class="sku-cell">${escapeHTML(item.display_sku)}</td><td>${escapeHTML(item.name)}</td><td class="number-cell">${item.initial_quantity}</td><td class="money-cell">${money(item.unit_price_cents)}</td><td class="available">${item.initial_quantity - item.sold_quantity}</td>${buttons}</tr>`;
  }).join("");
  return `<div class="table-wrap"><table class="item-table"><thead><tr><th>编号</th><th>商品</th><th>数量</th><th>单价</th><th>可售</th>${actionHeading}</tr></thead><tbody>${rows}</tbody></table></div>`;
}

function activeView(current) {
  const availableItems = current.items.filter(item => item.initial_quantity > item.sold_quantity);
  const summary = state.summary || {};
  const settlement = state.settlement || {};
  return `<div class="sales-toolbar"><div><h3>登记销售</h3><span class="item-count">${availableItems.length} 个商品仍可售 / 版本 ${current.version}</span></div><span class="state-pill state-active">销售中</span></div><div class="live-summary"><div><span>累计销售</span><strong>${money(summary.gross_sales_cents || 0)}</strong></div><div><span>售出进度</span><strong>${percent(summary.sell_through_bps || 0)}</strong></div><div><span>待退数量</span><strong>${settlement.returned_quantity || 0}</strong></div><div><span>预计应付</span><strong>${money(settlement.seller_payout_cents || 0)}</strong></div></div><form class="sales-form" id="sales-form"><div class="field"><label for="sale-sku">商品编号</label><input id="sale-sku" name="sku" list="available-skus" placeholder="输入或选择编号" required><datalist id="available-skus">${availableItems.map(item => `<option value="${escapeHTML(item.display_sku)}">${escapeHTML(item.name)}</option>`).join("")}</datalist></div><div class="field"><label for="sale-quantity">售出数量</label><input id="sale-quantity" name="quantity" type="number" min="1" step="1" required></div><button class="button button-danger" type="submit" ${availableItems.length ? "" : "disabled"}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 12h18M13 6l6 6-6 6"/></svg>登记销售</button></form><div class="activity-heading"><h3>销售记录</h3><span>${current.sales.length} 笔已提交</span></div>${salesRows(current)}<div class="action-zone"><button class="button button-secondary" id="settle-button" type="button"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M7 4v3m10-3v3M5 10h14v9H5z"/></svg>确认退还并结算</button></div>`;
}

function salesRows(current) {
  if (!current.sales.length) return '<div class="empty-state"><strong>还没有销售记录</strong><span>每次销售单独登记，库存会同步减少。</span></div>';
  return current.sales.slice().reverse().map(sale => `<div class="sale-row"><div><strong>${escapeHTML(sale.sku)}</strong><span>幂等键 ${escapeHTML(sale.idempotency_key)}</span></div><div><strong>${sale.quantity} 件</strong><span>版本 ${sale.resulting_version}</span></div><div class="sale-amount">${money(sale.gross_cents)}</div></div>`).join("");
}

function settledView(current) {
  const receipt = current.receipt;
  return `<div class="settlement-banner"><div><strong>结算凭据已锁定</strong><span>${formatDate(receipt.settled_at)} · 最终版本 ${receipt.final_version}</span></div><span class="state-pill state-settled">已结算</span></div><div class="receipt-summary"><div class="receipt-stat"><span>销售总额</span><strong>${money(receipt.gross_cents)}</strong></div><div class="receipt-stat"><span>佣金</span><strong>${money(receipt.commission_cents)}</strong></div><div class="receipt-stat payout-stat"><span>应付摊主</span><strong>${money(receipt.seller_payout_cents)}</strong></div></div><div class="activity-heading"><h3>结算明细</h3><span>未售商品已计入退还</span></div><div class="table-wrap"><table class="item-table"><thead><tr><th>商品</th><th>售出</th><th>退还</th><th>销售额</th></tr></thead><tbody>${receipt.lines.map(line => `<tr><td class="sku-cell">${escapeHTML(line.display_sku)}<br><small>${escapeHTML(line.name)}</small></td><td>${line.sold_quantity}</td><td>${line.returned_quantity}</td><td class="money-cell">${money(line.sales_cents)}</td></tr>`).join("")}</tbody></table></div>`;
}

function bindCurrentActions(current) {
  const termsForm = document.querySelector("#terms-form");
  if (termsForm) termsForm.addEventListener("submit", event => updateConsignment(event, current));
  const itemForm = document.querySelector("#item-form");
  if (itemForm) itemForm.addEventListener("submit", event => addItem(event, current));
  const activate = document.querySelector("#activate-button");
  if (activate) activate.addEventListener("click", () => activateConsignment(current));
  const salesForm = document.querySelector("#sales-form");
  if (salesForm) salesForm.addEventListener("submit", event => recordSale(event, current));
  const settle = document.querySelector("#settle-button");
  if (settle) settle.addEventListener("click", () => settleConsignment(current));
  const cancelEdit = document.querySelector("#cancel-edit");
  if (cancelEdit) cancelEdit.addEventListener("click", () => {
    state.editingSKU = null;
    renderWorkspace();
  });
  document.querySelectorAll(".edit-item").forEach(button => button.addEventListener("click", () => {
    state.editingSKU = button.dataset.sku;
    renderWorkspace();
    document.querySelector("#item-sku").focus();
  }));
  document.querySelectorAll(".remove-item").forEach(button => button.addEventListener("click", () => removeItem(current, button.dataset.sku)));
}

async function updateConsignment(event, current) {
  event.preventDefault();
  const form = new FormData(event.target);
  try {
    await request(`/api/consignments/${encodeURIComponent(current.id)}`, { method: "PUT", body: JSON.stringify({ version: current.version, seller_name: form.get("seller_name"), commission_bps: Number(form.get("commission_bps")) }) });
    await loadList(false);
    toast("基本信息已更新");
  } catch (error) {
    toast(error.message);
  }
}

async function createConsignment(event) {
  event.preventDefault();
  const form = new FormData(event.target);
  try {
    const payload = await request("/api/consignments", { method: "POST", body: JSON.stringify({ seller_name: form.get("seller_name"), commission_bps: Number(form.get("commission_bps")) }) });
    state.selectedId = payload.consignment.id;
    state.filter.state = "";
    state.filter.query = "";
    document.querySelector("#list-state").value = "";
    document.querySelector("#list-query").value = "";
    await loadList(false);
    toast("草稿已建立");
  } catch (error) {
    toast(error.message);
  }
}

async function addItem(event, current) {
  event.preventDefault();
  const form = new FormData(event.target);
  try {
    const body = JSON.stringify({ version: current.version, sku: form.get("sku"), name: form.get("name"), quantity: Number(form.get("quantity")), unit_price: form.get("unit_price") });
    const editing = state.editingSKU;
    const path = editing ? `/api/consignments/${encodeURIComponent(current.id)}/items/${encodeURIComponent(editing)}` : `/api/consignments/${encodeURIComponent(current.id)}/items`;
    await request(path, { method: editing ? "PUT" : "POST", body });
    state.editingSKU = null;
    await loadList(false);
    toast(editing ? "商品已更新" : "商品已加入清单");
  } catch (error) {
    toast(error.message);
  }
}

async function removeItem(current, sku) {
  const item = current.items.find(entry => entry.sku === sku);
  if (!item || !window.confirm(`确认删除“${item.name}”吗？`)) return;
  try {
    await request(`/api/consignments/${encodeURIComponent(current.id)}/items/${encodeURIComponent(sku)}`, { method: "DELETE", body: JSON.stringify({ version: current.version }) });
    if (state.editingSKU === sku) state.editingSKU = null;
    await loadList(false);
    toast("商品已删除");
  } catch (error) {
    toast(error.message);
  }
}

async function activateConsignment(current) {
  try {
    await request(`/api/consignments/${encodeURIComponent(current.id)}/activate`, { method: "POST", body: JSON.stringify({ version: current.version }) });
    await loadList(false);
    toast("寄售单已上架");
  } catch (error) {
    toast(error.message);
  }
}

async function recordSale(event, current) {
  event.preventDefault();
  const form = new FormData(event.target);
  try {
    await request(`/api/consignments/${encodeURIComponent(current.id)}/sales`, { method: "POST", body: JSON.stringify({ version: current.version, idempotency_key: saleKey(), sku: form.get("sku"), quantity: Number(form.get("quantity")) }) });
    await loadList(false);
    toast("销售已登记");
  } catch (error) {
    toast(error.message);
  }
}

async function settleConsignment(current) {
  if (!window.confirm("确认未售商品已经退还，并锁定结算凭据吗？")) return;
  try {
    await request(`/api/consignments/${encodeURIComponent(current.id)}/settle`, { method: "POST", body: JSON.stringify({ version: current.version }) });
    await loadList(false);
    toast("结算凭据已生成");
  } catch (error) {
    toast(error.message);
  }
}

function saleKey() {
  return `sale-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

function stateLabel(value) {
  return value === "active" ? "销售中" : value === "settled" ? "已结算" : "草稿";
}

function money(cents) {
  return `¥${(Number(cents) / 100).toFixed(2)}`;
}

function percent(bps) {
  return `${(Number(bps) / 100).toFixed(2)}%`;
}

function formatDate(value) {
  return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(value));
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>'"]/g, char => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" }[char]));
}

function toast(message) {
  elements.toast.textContent = message;
  elements.toast.classList.add("show");
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => elements.toast.classList.remove("show"), 2800);
}
