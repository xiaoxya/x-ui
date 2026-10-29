// Screen reader and keyboard support for the ant-design-vue 1.x components the
// panel is built from. The components are adjusted once here, so every page
// and every icon antd draws by itself gets the same behavior:
// - icons are no longer read as "icon: <type>"; decorative ones are hidden,
// - clickable icons and tags are buttons: focusable and usable with Enter/Space,
// - clickable icons and buttons with only an icon, "+" or "-" are named,
// - a tooltip around a nameless button or icon names it,
// - dropdowns and popconfirms opened from the keyboard can be used with it,
// - form controls are named after their label and described by its tooltip,
//   setting rows' controls after their title and by their description,
// - selects are comboboxes where focus is, announcing the active option.
// Names come from aria-label when the template gives one, then the tooltip,
// then a11yIconLabels (icon type -> translated name, set by the page).

(function () {
    const iconLabels = typeof a11yIconLabels === 'undefined' ? {} : a11yIconLabels;
    const components = Vue.options.components;

    // An attribute marking names given here, holding the icon's name, which
    // a tooltip may replace and the label or divider around may complete.
    const autoName = 'data-a11y-name';
    // An attribute holding a form label's tooltip text, its hint.
    const hintText = 'data-a11y-hint';

    function onActivateKey(event) {
        if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            event.currentTarget.click();
        }
    }

    function withListener(on, name, listener) {
        const merged = Object.assign({}, on);
        merged[name] = on && on[name] ? [].concat(on[name], listener) : listener;
        return merged;
    }

    function describeIcon(vm, vnode) {
        if (!vnode || !vnode.data) {
            return vnode;
        }
        const attrs = vnode.data.attrs = Object.assign({}, vnode.data.attrs);
        const name = vm.$attrs['aria-label'];
        if (vm.$listeners.click) {
            attrs.role = 'button';
            if (attrs.tabIndex === -1) {
                attrs.tabIndex = 0;
            }
            attrs['aria-label'] = name || iconLabels[vm.type];
            if (!name) {
                attrs[autoName] = attrs['aria-label'];
            }
            vnode.data.on = withListener(vnode.data.on, 'keydown', onActivateKey);
        } else if (name) {
            attrs.role = 'img';
            attrs['aria-label'] = name;
        } else {
            delete attrs['aria-label'];
            attrs['aria-hidden'] = 'true';
        }
        // An undefined tabIndex makes Vue remove the attribute on every
        // update, also the one a popup trigger below gives the icon.
        if (attrs.tabIndex === undefined) {
            delete attrs.tabIndex;
        }
        return vnode;
    }

    // AIcon renders its <i> inside a LocaleReceiver slot.
    const Icon = components.AIcon.options;
    const iconRender = Icon.render;
    Icon.render = function (h) {
        const vnode = iconRender.call(this, h);
        const slot = vnode.data.scopedSlots.default;
        vnode.data.scopedSlots.default = locale => describeIcon(this, slot(locale));
        return vnode;
    };

    // Buttons antd draws with only an icon, like the alert's close button.
    function nameIconButton() {
        const parent = this.$el && this.$el.parentElement;
        const name = iconLabels[this.type];
        if (name && parent && parent.matches('button, a') && isNameless(parent)) {
            parent.setAttribute('aria-label', name);
            parent.setAttribute(autoName, name);
        }
    }
    Icon.mounted = [].concat(Icon.mounted || [], nameIconButton);

    // A tag with a click handler, like Stop or Logs on the overview.
    function tagAsButton() {
        const el = this.$el;
        if (this.$listeners.click && el && el.nodeType === 1 && !el.hasAttribute('role')) {
            el.setAttribute('role', 'button');
            el.tabIndex = 0;
            el.addEventListener('keydown', onActivateKey);
        }
    }
    const Tag = components.ATag.options;
    Tag.mounted = [].concat(Tag.mounted || [], tagAsButton);
    Tag.updated = [].concat(Tag.updated || [], tagAsButton);

    const popups = '.ant-popover:not(.ant-popover-hidden), .ant-dropdown:not(.ant-dropdown-hidden)';

    // Keys inside a popup opened from the keyboard: Escape closes it, and
    // after a button or menu item closes it focus goes back to its trigger.
    function onPopupKey(event) {
        const popup = event.currentTarget;
        const trigger = popup.a11yTrigger;
        if (!trigger) {
            return;
        }
        if (event.key === 'Escape') {
            trigger.click();
            trigger.focus();
        } else if (event.key === 'Enter' || event.key === ' ') {
            const restore = () => {
                const active = document.activeElement;
                if (!active || active === document.body || popup.contains(active)) {
                    trigger.focus();
                }
            };
            // Listening before the menu runs the item's action, which may
            // open a modal that later returns focus where it was; a button
            // runs its action after keydown.
            if (popup.querySelector('.ant-dropdown-menu')) {
                restore();
            } else {
                setTimeout(restore);
            }
        }
    }

    // Antd opens these popups at the end of the page, so after opening one
    // from the keyboard focus goes to its buttons or menu.
    function focusPopup(trigger, tries) {
        const popup = [...document.querySelectorAll(popups)].pop();
        const target = popup && popup.querySelector('.ant-popover-buttons .ant-btn, .ant-dropdown-menu');
        if (target) {
            if (!popup.a11yTrigger) {
                popup.addEventListener('keydown', onPopupKey, true);
            }
            popup.a11yTrigger = trigger;
            target.focus();
        } else if (tries > 0) {
            setTimeout(() => focusPopup(trigger, tries - 1), 50);
        }
    }

    // An icon opening a popconfirm or a dropdown gets its click handler from
    // them, so it isn't marked as a button while rendering.
    function iconTriggerAsButton(el) {
        el.setAttribute('role', 'button');
        el.tabIndex = 0;
        el.removeAttribute('aria-hidden');
        el.addEventListener('keydown', onActivateKey);
        const type = (el.className.match(/\banticon-(?!spin\b)([\w-]+)/) || [])[1];
        if (!el.getAttribute('aria-label') && iconLabels[type]) {
            el.setAttribute('aria-label', iconLabels[type]);
            el.setAttribute(autoName, iconLabels[type]);
        }
    }

    function popupTrigger() {
        const el = this.$el;
        if (!el || el.nodeType !== 1 || el.a11yPopupTrigger) {
            return;
        }
        el.a11yPopupTrigger = true;
        if (el.matches('.anticon') && el.getAttribute('role') !== 'button') {
            iconTriggerAsButton(el);
        }
        el.addEventListener('keydown', event => {
            if (event.key === 'Enter' || event.key === ' ') {
                setTimeout(() => focusPopup(el, 10), 50);
            }
        });
    }
    ['APopconfirm', 'ADropdown'].forEach(name => {
        const trigger = components[name].options;
        trigger.mounted = [].concat(trigger.mounted || [], popupTrigger);
        trigger.updated = [].concat(trigger.updated || [], popupTrigger);
    });

    function hasText(nodes) {
        return (nodes || []).some(node => {
            if (!node.tag) {
                return !!(node.text && node.text.trim());
            }
            if (node.componentOptions) {
                return node.componentOptions.tag !== 'a-icon';
            }
            return hasText(node.children);
        });
    }

    function slotIcon(nodes) {
        const icon = (nodes || []).find(node => node.componentOptions && node.componentOptions.tag === 'a-icon');
        return icon && icon.componentOptions.propsData.type;
    }

    // Buttons showing only "+" or "-" are named like these icons.
    const signs = { '+': 'plus', '-': 'minus' };

    const Button = components.AButton.options;
    const buttonRender = Button.render;
    Button.render = function (h) {
        const vnode = buttonRender.call(this, h);
        if (vnode && vnode.data && !this.$attrs['aria-label']) {
            const slot = this.$slots.default;
            const name = iconLabels[hasText(slot) ? signs[vnodeText(slot).trim()] : this.icon || slotIcon(slot)];
            if (name) {
                vnode.data.attrs = Object.assign({}, vnode.data.attrs, { 'aria-label': name, [autoName]: name });
            }
        }
        return vnode;
    };

    function vnodeText(nodes) {
        return (nodes || []).map(node => {
            if (!node.tag) {
                return node.text || '';
            }
            return vnodeText(node.children || (node.componentOptions && node.componentOptions.children));
        }).join(' ');
    }

    function tooltipText(vm) {
        const title = typeof vm.title === 'string' ? vm.title : vnodeText(vm.$slots.title || vm.title);
        return (title || '').replace(/\s+/g, ' ').trim();
    }

    function isNameless(el) {
        if (el.hasAttribute(autoName)) {
            return true;
        }
        return !el.getAttribute('aria-label') && !el.textContent.trim();
    }

    // The tooltip's text names the element it wraps when that element is a
    // control without a name of its own, or a lone icon carrying information.
    // Tooltips antd uses inside popovers and popconfirms are left alone.
    function nameByTooltip() {
        if (/^A[A-Z]/.test(this.$vnode.context.$options.name || '')) {
            return;
        }
        const el = this.$el;
        const text = el && el.nodeType === 1 && tooltipText(this);
        if (!text) {
            return;
        }
        if (el.matches('button, a, [role="button"]') && isNameless(el)) {
            el.setAttribute('aria-label', text);
            el.removeAttribute(autoName);
        } else if (el.closest('.ant-form-item-label')) {
            el.setAttribute(hintText, text);
        } else if (el.matches('.anticon[aria-hidden]')) {
            el.removeAttribute('aria-hidden');
            el.setAttribute('role', 'img');
            el.setAttribute('aria-label', text);
        }
    }

    const Tooltip = components.ATooltip.options;
    Tooltip.mounted = [].concat(Tooltip.mounted || [], nameByTooltip);
    Tooltip.updated = [].concat(Tooltip.updated || [], nameByTooltip);

    // Form controls named after their form item's label. Antd 1.x only ties
    // the two together for forms using v-decorator, which the panel doesn't.
    const formControls = [
        'input:not([type="hidden"]):not([type="radio"]):not([type="checkbox"])',
        'textarea',
        '[role="combobox"]',
        '[role="switch"]',
        '.ant-checkbox-wrapper',
        '.ant-radio-group',
    ].join(', ');

    // The label's text without the buttons in it, like the sync icon next to
    // "Email" or "Password".
    function labelText(node) {
        return [...node.childNodes].map(child => {
            if (child.nodeType === Node.TEXT_NODE) {
                return child.textContent;
            }
            if (child.nodeType !== Node.ELEMENT_NODE || child.getAttribute('aria-hidden') === 'true' ||
                child.matches('button, [role="button"]')) {
                return '';
            }
            return labelText(child);
        }).join(' ').replace(/\s+/g, ' ').trim();
    }

    // Tooltip texts are only in the page while hovered, so the hint of a
    // label is kept in a hidden element the controls are described by.
    let hints;
    let hintCount = 0;
    function hintElement(vm, text) {
        if (!hints) {
            hints = document.createElement('div');
            hints.hidden = true;
            document.body.appendChild(hints);
        }
        if (!vm.a11yHint) {
            vm.a11yHint = document.createElement('span');
            vm.a11yHint.id = 'a11y-hint-' + (++hintCount);
            hints.appendChild(vm.a11yHint);
            vm.$once('hook:beforeDestroy', () => vm.a11yHint.remove());
        }
        if (vm.a11yHint.textContent !== text) {
            vm.a11yHint.textContent = text;
        }
        return vm.a11yHint.id;
    }

    function setAttribute(el, name, value) {
        if (!value) {
            el.removeAttribute(name);
        } else if (el.getAttribute(name) !== value) {
            el.setAttribute(name, value);
        }
    }

    // The parts of a page whose label or title names what is in them.
    const scopes = '.ant-form-item, .ant-list-item, .ant-divider';

    // An icon button named after its icon gets the text it belongs to, like
    // "Add" next to the "Fallbacks" label or "Delete" in "Fallback 1".
    function nameInContext(el, context, container = el) {
        if (!context) {
            return;
        }
        container.querySelectorAll('button[' + autoName + '], [role="button"][' + autoName + ']').forEach(button => {
            const base = button.getAttribute(autoName);
            if (base && button.closest(scopes) === el) {
                setAttribute(button, 'aria-label', base + ' ' + context);
            }
        });
    }

    function nameDividerButtons() {
        const el = this.$el;
        if (el && el.nodeType === 1) {
            nameInContext(el, labelText(el));
        }
    }

    const Divider = components.ADivider.options;
    Divider.mounted = [].concat(Divider.mounted || [], nameDividerButtons);
    Divider.updated = [].concat(Divider.updated || [], nameDividerButtons);

    // Names the controls of a form or list item after its label or title,
    // and describes them by its hint or description.
    function nameControls(el, container, name, describedBy) {
        nameInContext(el, name, container);
        container.querySelectorAll(formControls).forEach(control => {
            if (control.closest(scopes) !== el) {
                return;
            }
            if (control.matches('.ant-checkbox-wrapper')) {
                if (control.textContent.trim()) {
                    return;
                }
                control = control.querySelector('input');
            } else if (control.matches('.ant-radio-group')) {
                control.setAttribute('role', 'radiogroup');
            }
            const own = control.hasAttribute('aria-labelledby') ||
                (control.hasAttribute('aria-label') && !control.hasAttribute(autoName));
            if (!own) {
                setAttribute(control, 'aria-label', name);
                control.setAttribute(autoName, '');
            }
            const described = control.getAttribute('aria-describedby');
            if (!described || described.startsWith('a11y-')) {
                setAttribute(control, 'aria-describedby', describedBy);
            }
        });
    }

    function linkFormItem() {
        const el = this.$el;
        const label = el && el.nodeType === 1 && el.querySelector(':scope > .ant-form-item-label label');
        const wrapper = label && el.querySelector(':scope > .ant-form-item-control-wrapper');
        if (!wrapper) {
            return;
        }
        const hint = label.querySelector('[' + hintText + ']');
        nameControls(el, wrapper, labelText(label), hint ? hintElement(this, hint.getAttribute(hintText)) : '');
    }

    const FormItem = components.AFormItem.options;
    FormItem.mounted = [].concat(FormItem.mounted || [], linkFormItem);
    FormItem.updated = [].concat(FormItem.updated || [], linkFormItem);

    // Setting rows: a list item's title names the controls next to it, and
    // its description describes them. Some list items hold several rows.
    let descriptionCount = 0;
    function linkListItem() {
        const el = this.$el;
        if (!el || el.nodeType !== 1) {
            return;
        }
        el.querySelectorAll('.ant-list-item-meta-title').forEach(title => {
            if (title.closest('.ant-list-item') !== el) {
                return;
            }
            let row = title.parentElement;
            while (row !== el && !row.querySelector(formControls)) {
                row = row.parentElement;
            }
            const description = title.closest('.ant-list-item-meta').querySelector('.ant-list-item-meta-description');
            if (description && !description.id) {
                description.id = 'a11y-description-' + (++descriptionCount);
            }
            nameControls(el, row, labelText(title), description && description.textContent.trim() ? description.id : '');
        });
    }

    const ListItem = components.AListItem.options;
    ListItem.mounted = [].concat(ListItem.mounted || [], linkListItem);
    ListItem.updated = [].concat(ListItem.updated || [], linkListItem);

    // Antd's select takes focus on its outer box, or on the search input of
    // a multiple select, but gives the combobox role to an inner box that is
    // never focused, so screen readers announced neither the select nor the
    // options the arrow keys move through. The focused element becomes the
    // combobox, pointing at the active option.
    let optionCount = 0;
    function updateSelect(vm) {
        const el = vm.$el;
        const inner = el && el.nodeType === 1 && el.querySelector('.ant-select-selection');
        const target = inner && (el.tabIndex >= 0 ? el : el.querySelector('.ant-select-search__field'));
        if (!target) {
            return;
        }
        const listId = inner.getAttribute('aria-controls');
        const open = el.classList.contains('ant-select-open');
        const listbox = open && listId && document.getElementById(listId);
        const active = listbox && listbox.querySelector('.ant-select-dropdown-menu-item-active');
        if (active && !active.id) {
            active.id = 'a11y-option-' + (++optionCount);
        }
        inner.setAttribute('role', 'none');
        target.setAttribute('role', 'combobox');
        setAttribute(target, 'aria-haspopup', 'listbox');
        setAttribute(target, 'aria-controls', listId);
        setAttribute(target, 'aria-expanded', open ? 'true' : 'false');
        setAttribute(target, 'aria-activedescendant', active ? active.id : '');
        if (vm.$attrs['aria-label']) {
            setAttribute(target, 'aria-label', vm.$attrs['aria-label']);
        }
        if (!target.a11ySelect) {
            target.a11ySelect = true;
            target.addEventListener('keydown', () => setTimeout(() => updateSelect(vm)));
        }
    }

    function selectAsCombobox() {
        updateSelect(this);
        // the dropdown is drawn after the select opens
        setTimeout(() => updateSelect(this), 50);
    }

    const Select = components.ASelect.options;
    Select.mounted = [].concat(Select.mounted || [], selectAsCombobox);
    Select.updated = [].concat(Select.updated || [], selectAsCombobox);
})();
