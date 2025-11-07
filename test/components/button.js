// components/button.js
class ButtonComponent {
    constructor(selector) {
        this.element = document.querySelector(selector);
        this.init();
    }

    init() {
        if (this.element) {
            this.element.addEventListener('click', this.handleClick.bind(this));
            console.log('Button component initialized');
        }
    }

    handleClick(event) {
        event.preventDefault();
        console.log('Button clicked');
        this.element.classList.add('clicked');
        setTimeout(() => {
            this.element.classList.remove('clicked');
        }, 300);
    }
}