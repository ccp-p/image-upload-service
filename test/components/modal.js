// components/modal.js
class ModalComponent {
    constructor(options = {}) {
        this.options = {
            selector: '.modal',
            backdrop: true,
            ...options
        };
        this.init();
    }

    init() {
        this.createModalStructure();
        this.bindEvents();
        console.log('Modal component initialized');
    }

    createModalStructure() {
        const modal = document.createElement('div');
        modal.className = 'modal-overlay';
        modal.innerHTML = `
            <div class="modal-content">
                <button class="modal-close">&times;</button>
                <div class="modal-body">
                    <h3>模态框内容</h3>
                    <p>这是一个测试用的模态框组件</p>
                </div>
            </div>
        `;
        document.body.appendChild(modal);
    }

    bindEvents() {
        const closeBtn = document.querySelector('.modal-close');
        if (closeBtn) {
            closeBtn.addEventListener('click', this.close.bind(this));
        }
    }

    open() {
        document.querySelector('.modal-overlay').style.display = 'flex';
    }

    close() {
        document.querySelector('.modal-overlay').style.display = 'none';
    }
}